package pilot

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const releaseBase = "https://raw.githubusercontent.com/punisher89/my.kn-test-/refs/heads/packages"

type releaseDocument struct {
	EngineVersion  string    `json:"engine_version"`
	EngineSHA256   string    `json:"engine_sha256"`
	Kind           string    `json:"kind"`
	Version        string    `json:"version"`
	ManifestSHA256 string    `json:"manifest_sha256"`
	PublishedAt    time.Time `json:"published_at"`
}
type UpdateStatus struct {
	Engine       string `json:"engine,omitempty"`
	LatestEngine string `json:"latest_engine,omitempty"`
	Supported    bool   `json:"supported"`
	Current      string `json:"current"`
	Latest       string `json:"latest,omitempty"`
	Available    bool   `json:"available"`
	Running      bool   `json:"running"`
	Checking     bool   `json:"checking"`
	Stage        string `json:"stage"`
	Target       string `json:"target,omitempty"`
	PID          int    `json:"pid,omitempty"`
	Job          string `json:"job,omitempty"`
	Phase        string `json:"phase,omitempty"`
	ExitCode     int    `json:"exit_code,omitempty"`
}

var versionPattern = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)-alpha\.(\d+)$`)

func newerVersion(a, b string) bool {
	ma, mb := versionPattern.FindStringSubmatch(a), versionPattern.FindStringSubmatch(b)
	if ma == nil || mb == nil {
		return false
	}
	for i := 1; i <= 4; i++ {
		x, ex := strconv.ParseUint(ma[i], 10, 32)
		y, ey := strconv.ParseUint(mb[i], 10, 32)
		if ex != nil || ey != nil {
			return false
		}
		if x != y {
			return x > y
		}
	}
	return false
}
func validRelease(d releaseDocument) error {
	if len(d.EngineSHA256) != 64 || d.EngineVersion == "" {
		return errors.New("в выпуске нет закреплённого движка")
	}
	if _, e := hex.DecodeString(d.EngineSHA256); e != nil {
		return e
	}
	if d.Kind != "pilot-release" || !versionPattern.MatchString(d.Version) || len(d.ManifestSHA256) != 64 || d.PublishedAt.IsZero() {
		return errors.New("некорректные сведения о выпуске")
	}
	_, e := hex.DecodeString(d.ManifestSHA256)
	return e
}
func (m *Manager) updatePrefix() (string, error) {
	c := m.Config()
	app := filepath.Dir(c.EngineDir)
	prefix := filepath.Dir(app)
	if m.backend.Kind() != "router" || filepath.Base(app) != "keenetic-pilot" || filepath.Clean(prefix) != prefix || prefix == "/" || !filepath.IsAbs(prefix) || strings.Contains(prefix, "..") || !regexp.MustCompile(`^/[a-zA-Z0-9_./-]+$`).MatchString(prefix) {
		return "", errors.New("обновление доступно в установленной службе Pilot")
	}
	if prefix == "/opt" && os.Geteuid() != 0 {
		return "", errors.New("для обновления Entware нужен root")
	}
	if filepath.Clean(m.path) != filepath.Join(prefix, "etc/keenetic-pilot/config.json") {
		return "", errors.New("нештатное расположение конфигурации")
	}
	marker, e := os.ReadFile(filepath.Join(app, ".pilot-managed"))
	if e != nil || strings.TrimSpace(string(marker)) != "keenetic-pilot" {
		return "", errors.New("не подтверждена управляемая установка")
	}
	init, e := os.ReadFile(filepath.Join(prefix, "etc/init.d/S98keenetic-pilot"))
	if e != nil || !strings.Contains(string(init), "Keenetic Pilot") {
		return "", errors.New("служба обновления не найдена")
	}
	pid, e := os.ReadFile(filepath.Join(prefix, "var/run/keenetic-pilot.pid"))
	if e != nil || strings.TrimSpace(string(pid)) != strconv.Itoa(os.Getpid()) {
		return "", errors.New("контроллер запущен вне управляемой службы")
	}
	return prefix, nil
}
func (m *Manager) Updates() UpdateStatus {
	_, err := m.updatePrefix()
	s := UpdateStatus{Supported: err == nil, Current: Version, Stage: "idle"}
	s.Checking = m.updateChecking.Load()
	if b, e := os.ReadFile(filepath.Join(m.Config().EngineDir, "VERSION")); e == nil && len(b) < 100 {
		v := strings.TrimSpace(string(b))
		if regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`).MatchString(v) {
			s.Engine = v
		}
	}
	m.mu.Lock()
	if m.release != nil {
		s.Latest = m.release.Version
		s.LatestEngine = m.release.EngineVersion
		s.Available = newerVersion(s.Latest, Version)
	}
	m.mu.Unlock()
	b, e := os.ReadFile(filepath.Join(filepath.Dir(m.path), "update-status.json"))
	if e == nil && len(b) < 8192 {
		var saved UpdateStatus
		if json.Unmarshal(b, &saved) == nil {
			s.Running = saved.Running
			s.Stage = saved.Stage
			s.Target = saved.Target
			s.PID = saved.PID
			s.Job = saved.Job
			switch saved.Phase {
			case "log-open", "waiting", "prepare", "worker-start", "install", "version-check":
				s.Phase = saved.Phase
			}
			if saved.ExitCode > 0 && saved.ExitCode <= 255 {
				s.ExitCode = saved.ExitCode
			}
		}
	}
	if s.Running && !m.updating.Load() {
		b, e := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", s.PID))
		if s.PID < 1 || e != nil || s.Job == "" || !strings.Contains(string(b), s.Job) {
			s.Running = false
			s.Stage = "interrupted"
		}
	}
	if m.updateRequested.Load() && !m.updating.Load() {
		s.Running = true
		s.Stage = "waiting"
	}
	// Paths and process identifiers remain private; only control logic uses them.
	s.PID = 0
	s.Job = ""
	return s
}
func (m *Manager) CheckUpdate(ctx context.Context) error {
	// Fetching signed metadata neither changes the running engine nor needs
	// the operation lock held by a potentially six-minute domain search.
	if !m.updateChecking.CompareAndSwap(false, true) {
		return ErrBusy
	}
	defer m.updateChecking.Store(false)
	token, e := githubReadToken(m.path)
	if e != nil {
		return e
	}
	b, e := fetchCatalog(ctx, releaseBase+"/latest.json", token)
	if e != nil {
		return e
	}
	sig, e := fetchCatalog(ctx, releaseBase+"/latest.json.sig", token)
	if e != nil {
		return e
	}
	var d releaseDocument
	if e = signedJSON(b, sig, updatesPublicKey, &d); e != nil {
		return e
	}
	if e = validRelease(d); e != nil {
		return e
	}
	m.mu.Lock()
	m.release = &d
	m.mu.Unlock()
	return nil
}
func (m *Manager) StartUpdate(version string) error {
	prefix, e := m.updatePrefix()
	if e != nil {
		return e
	}
	if m.Updates().Running {
		return ErrBusy
	}
	m.mu.Lock()
	var doc releaseDocument
	if m.release != nil {
		doc = *m.release
	}
	m.mu.Unlock()
	if doc.Version != version || !newerVersion(doc.Version, Version) {
		return errors.New("сначала проверьте и выберите доступное обновление")
	}
	if !m.updateRequested.CompareAndSwap(false, true) {
		return ErrBusy
	}
	path := filepath.Join(filepath.Dir(m.path), "update-status.json")
	if e = WriteJSON(path, UpdateStatus{Running: true, Stage: "waiting", Target: version}); e != nil {
		m.updateRequested.Store(false)
		return e
	}
	go func() {
		defer m.updateRequested.Store(false)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		log, e := os.OpenFile(filepath.Join(filepath.Dir(m.path), "update.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC|os.O_APPEND, 0600)
		if e != nil {
			m.log.Add("Не удалось открыть журнал обновления: " + e.Error())
			_ = WriteJSON(path, UpdateStatus{Stage: "failed", Target: version, Phase: "log-open"})
			return
		}
		defer log.Close()
		fmt.Fprintf(log, "Preparing Pilot update to %s\n", version)
		fail := func(phase string, err error) {
			fmt.Fprintf(log, "Update failed during %s: %v\n", phase, err)
			_ = WriteJSON(path, UpdateStatus{Stage: "failed", Target: version, Phase: phase})
		}
		// A user-requested update gets the next free operation slot. Stop
		// tuning through its context and wait for its normal cleanup, without
		// unlocking another goroutine's mutex or stopping live traffic here.
		if e = m.waitUpdateOperation(ctx); e != nil {
			fail("waiting", e)
			return
		}
		defer m.op.Unlock()
		// Serialize this handover with Close setting closed, so shutdown
		// either waits for this short abort or sees an active updater.
		m.mu.Lock()
		if m.closed {
			m.mu.Unlock()
			fail("waiting", errors.New("контроллер завершает работу"))
			return
		}
		m.updating.Store(true)
		m.mu.Unlock()
		defer m.updating.Store(false)
		if e = WriteJSON(path, UpdateStatus{Running: true, Stage: "downloading", Target: version}); e != nil {
			fail("prepare", e)
			return
		}
		job, e := os.MkdirTemp(filepath.Dir(m.path), "update-job-")
		if e != nil {
			fail("prepare", e)
			return
		}
		cleanup := true
		defer func() {
			if cleanup {
				os.RemoveAll(job)
			}
		}()
		if e = m.prepareUpdate(ctx, job, doc); e != nil {
			m.log.Add("Обновление не подготовлено: " + e.Error())
			fail("prepare", e)
			return
		}
		worker, e := os.ReadFile(filepath.Join(filepath.Dir(m.Config().EngineDir), "scripts/update-worker.sh"))
		if e != nil {
			fail("worker-start", e)
			return
		}
		workerPath := filepath.Join(job, "worker.sh")
		if e = os.WriteFile(workerPath, worker, 0700); e != nil {
			fail("worker-start", e)
			return
		}
		cmd := exec.Command("sh", workerPath, job, prefix, version)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		cmd.Stdout = log
		cmd.Stderr = log
		if e = cmd.Start(); e != nil {
			fail("worker-start", e)
			return
		}
		cleanup = false
		_ = cmd.Wait()
	}()
	return nil
}

func (m *Manager) waitUpdateOperation(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		m.mu.Lock()
		closed := m.closed
		if m.cancel != nil {
			m.cancel()
		}
		m.mu.Unlock()
		if closed {
			return errors.New("контроллер завершает работу")
		}
		if ctx.Err() != nil {
			return fmt.Errorf("ожидание завершения текущей операции: %w", ctx.Err())
		}
		if m.op.TryLock() {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("ожидание завершения текущей операции: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}
func (m *Manager) prepareUpdate(ctx context.Context, job string, doc releaseDocument) error {
	return m.prepareUpdateFrom(ctx, job, doc, releaseBase)
}

func (m *Manager) prepareUpdateFrom(ctx context.Context, job string, doc releaseDocument, base string) error {
	token, e := githubReadToken(m.path)
	if e != nil {
		return e
	}
	manifest, e := fetchCatalog(ctx, base+"/v"+doc.Version+"/SHA256SUMS", token)
	if e != nil {
		return e
	}
	sum := sha256.Sum256(manifest)
	if hex.EncodeToString(sum[:]) != doc.ManifestSHA256 {
		return errors.New("контрольная сумма манифеста не совпадает")
	}
	arch := runtime.GOARCH
	switch arch {
	case "amd64", "arm64", "arm", "mips", "mipsle":
	default:
		return errors.New("архитектура не поддерживается")
	}
	name := "keenetic-pilot-" + doc.Version + "-linux-" + arch + ".tar.gz"
	expected := ""
	matches := 0
	for _, line := range strings.Split(string(manifest), "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && (f[1] == name || f[1] == "./"+name) {
			matches++
			expected = f[0]
		}
	}
	if matches != 1 || len(expected) != 64 {
		return errors.New("пакет не найден в манифесте")
	}
	raw := base + "/v" + doc.Version + "/" + name
	req, e := http.NewRequestWithContext(ctx, "GET", raw, nil)
	if e != nil {
		return e
	}
	if token != "" {
		req.Header.Set("Authorization", "token "+token)
	}
	client := &http.Client{Timeout: 3 * time.Minute, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) > 3 || r.URL.Scheme != "https" || r.URL.Host != "raw.githubusercontent.com" {
			return errors.New("недопустимое перенаправление пакета")
		}
		return nil
	}}
	resp, e := client.Do(req)
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("пакет: HTTP %d", resp.StatusCode)
	}
	file, e := os.Create(filepath.Join(job, "package.tar.gz"))
	if e != nil {
		return e
	}
	hash := sha256.New()
	n, e := io.Copy(io.MultiWriter(file, hash), io.LimitReader(resp.Body, 64<<20+1))
	closeErr := file.Close()
	if e != nil {
		return e
	}
	if closeErr != nil {
		return closeErr
	}
	if n > 64<<20 || hex.EncodeToString(hash.Sum(nil)) != expected {
		return errors.New("контрольная сумма пакета не совпадает")
	}
	file, e = os.Open(filepath.Join(job, "package.tar.gz"))
	if e != nil {
		return e
	}
	defer file.Close()
	if e = extractUpdate(file, job); e != nil {
		return e
	}
	pin, e := os.ReadFile(filepath.Join(job, "keenetic-pilot/dependencies/nfqws2.env"))
	if e != nil {
		return e
	}
	if !strings.Contains(string(pin), "ENGINE_VERSION="+doc.EngineVersion+"\n") || !strings.Contains(string(pin), "ENGINE_SHA256="+doc.EngineSHA256+"\n") {
		return errors.New("движок пакета не совпадает с подписанным выпуском")
	}
	platform, e := os.ReadFile(filepath.Join(job, "keenetic-pilot/PLATFORM"))
	if e != nil || strings.TrimSpace(string(platform)) != arch {
		return errors.New("архитектура пакета не совпадает")
	}
	versionFile, e := os.ReadFile(filepath.Join(job, "keenetic-pilot/VERSION"))
	if e != nil || strings.TrimSpace(string(versionFile)) != doc.Version {
		return errors.New("версия пакета не совпадает с выпуском")
	}
	versionCmd := exec.CommandContext(ctx, filepath.Join(job, "keenetic-pilot/bin/pilot"), "--version")
	if out, e := versionCmd.Output(); e != nil || strings.TrimSpace(string(out)) != doc.Version {
		return errors.New("версия контроллера не совпадает с выпуском")
	}
	cmd := exec.CommandContext(ctx, filepath.Join(job, "keenetic-pilot/bin/pilot"), "--config", m.path, "--check-config")
	if out, e := cmd.CombinedOutput(); e != nil {
		return fmt.Errorf("новая версия не принимает конфигурацию: %w: %s", e, tail(out, 1000))
	}
	return nil
}
func extractUpdate(input io.Reader, dir string) error {
	gz, e := gzip.NewReader(input)
	if e != nil {
		return e
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var total int64
	count := 0
	for {
		h, e := tr.Next()
		if e == io.EOF {
			return nil
		}
		if e != nil {
			return e
		}
		count++
		name := strings.TrimSuffix(h.Name, "/")
		clean := filepath.Clean(name)
		if count > 4096 || name != clean || filepath.IsAbs(name) || !(name == "keenetic-pilot" || strings.HasPrefix(name, "keenetic-pilot/")) || strings.Contains(name, "\\") || h.Size < 0 {
			return errors.New("небезопасная структура пакета")
		}
		total += h.Size
		if total > 256<<20 {
			return errors.New("пакет слишком большой")
		}
		path := filepath.Join(dir, name)
		switch h.Typeflag {
		case tar.TypeDir:
			if e = os.MkdirAll(path, 0700); e != nil {
				return e
			}
		case tar.TypeReg:
			if e = os.MkdirAll(filepath.Dir(path), 0700); e != nil {
				return e
			}
			f, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, os.FileMode(h.Mode)&0755)
			if e != nil {
				return e
			}
			_, copyErr := io.CopyN(f, tr, h.Size)
			closeErr := f.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		default:
			return errors.New("ссылки и специальные файлы запрещены в пакете")
		}
	}
}
