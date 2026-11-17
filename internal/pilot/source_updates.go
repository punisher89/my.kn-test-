package pilot

// Secondary GitHub HTTPS + SHA-256 source. Signed channel remains untouched.
// The publisher labels are repository-provided labels, NOT cryptographic authorship
// proofs. This trust model is intentionally less strong than old Ed25519.
import (
  "bytes"
  "context"
  "encoding/hex"
  "encoding/json"
  "errors"
  "fmt"
  "os"
  "os/exec"
  "path/filepath"
  "regexp"
  "strings"
  "syscall"
  "time"
)

const sourceReleaseBase = "https://raw.githubusercontent.com/punisher89/my.kn-test-/refs/heads/ssh-releases"

type sourceReleaseDocument struct {
    Kind string `json:"kind"`
    Channel string `json:"channel"`
    Version string `json:"version"`
    Source string `json:"source"`
    GitSHA string `json:"git_sha"`
    PublishedAt time.Time `json:"published_at"`
    ManifestSHA256 string `json:"manifest_sha256"`
    InstallerSHA256 string `json:"installer_sha256"`
    EngineVersion string `json:"engine_version"`
    EngineSHA256 string `json:"engine_sha256"`
    SignedPanelUpdaterModified bool `json:"signed_panel_updater_modified"`
    InstalledVia string `json:"installed_via"`
}
type SourceUpdateView struct {
    Source string `json:"source"`
    Latest string `json:"latest,omitempty"`
    PublishedAt time.Time `json:"published_at,omitempty"`
    Commit string `json:"commit,omitempty"`
    Available bool `json:"available"`
    Checked bool `json:"checked"`
    Verification string `json:"verification"`
}
type SourceUpdateStatus struct {
    Current string `json:"current"`
    Supported bool `json:"supported"`
    Running bool `json:"running"`
    Stage string `json:"stage"`
    Sources []SourceUpdateView `json:"sources"`
}
func validUpdateSource(source string) bool {return source=="ChatGPT" || source=="Codex"}
func validSourceRelease(d sourceReleaseDocument, source string) error {
    if !validUpdateSource(source) || d.Kind!="pilot-ssh-release" || d.Channel!="ssh" || d.Source!=source || d.InstalledVia!="ssh" || d.SignedPanelUpdaterModified ||
        !versionPattern.MatchString(d.Version) || d.PublishedAt.IsZero() || d.PublishedAt.After(time.Now().Add(24*time.Hour)) ||
        !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(d.GitSHA) ||
        d.EngineVersion=="" || strings.ContainsAny(d.EngineVersion," /\\\r\n\t") {
        return errors.New("некорректный или неподдерживаемый выпуск ChatGPT / Codex")
    }
    for _, v := range []string{d.ManifestSHA256,d.InstallerSHA256,d.EngineSHA256} {
        if len(v)!=64 {return errors.New("неполный SHA-256 выпуска")}
        if _, err:=hex.DecodeString(v);err!=nil {return errors.New("некорректный SHA-256 выпуска")}
    }
    return nil
}
func (m *Manager) SourceUpdates() SourceUpdateStatus {
    signed:=m.Updates()
    result:=SourceUpdateStatus{Current:Version,Supported:signed.Supported,Running:signed.Running,Stage:signed.Stage,Sources:[]SourceUpdateView{}}
    m.mu.Lock()
    defer m.mu.Unlock()
    for _, source := range []string{"ChatGPT","Codex"} {
        item:=SourceUpdateView{Source:source,Verification:"github-https-sha256"}
        if doc,ok:=m.sourceReleases[source];ok {
            item.Latest=doc.Version
            item.Commit=doc.GitSHA
            item.PublishedAt=doc.PublishedAt
            item.Checked=true
            item.Available=newerVersion(doc.Version,Version)
        }
        result.Sources=append(result.Sources,item)
    }
    return result
}
func (m *Manager) CheckSourceUpdate(ctx context.Context, source string) error {
    if !validUpdateSource(source) {return errors.New("неизвестный источник обновления")}
    if !m.updateChecking.CompareAndSwap(false,true){return ErrBusy}
    defer m.updateChecking.Store(false)
    token,err:=githubReadToken(m.path)
    if err!=nil{return err}
    path:="/latest-"+strings.ToLower(source)+".json"
    raw,err:=fetchCatalog(ctx,sourceReleaseBase+path,token)
    if err!=nil{return fmt.Errorf("не удалось проверить выпуск %s: %w",source,err)}
    var doc sourceReleaseDocument
    dec:=json.NewDecoder(bytes.NewReader(raw))
    dec.DisallowUnknownFields()
    if err=dec.Decode(&doc);err!=nil{return err}
    if err=validSourceRelease(doc,source);err!=nil{return err}
    // The mutable discovery pointer must refer to an existing immutable
    // release folder. Both bytewise-equal metadata copies are HTTPS-fetched
    // from our specific GitHub repository, not from arbitrary URLs.
    original,err:=fetchCatalog(ctx,sourceReleaseBase+"/v"+doc.Version+"/release-info.json",token)
    if err!=nil{return err}
    if !bytes.Equal(bytes.TrimSpace(original),bytes.TrimSpace(raw)) {
        return errors.New("указатель источника не совпадает с выпуском")
    }
    m.mu.Lock()
    if m.sourceReleases==nil {m.sourceReleases=map[string]sourceReleaseDocument{}}
    m.sourceReleases[source]=doc
    m.mu.Unlock()
    return nil
}

// This does not call StartUpdate, change the signed channel or relax its
// Ed25519 verification. It uses the same exclusive operation handover,
// archive preflight, configuration check and detached worker as that channel.
func (m *Manager) StartSourceUpdate(source, version string) error {
	if !validUpdateSource(source) { return errors.New("неизвестный источник обновлений") }
	prefix, e := m.updatePrefix()
	if e != nil {
		return e
	}
	if m.Updates().Running {
		return ErrBusy
	}
	m.mu.Lock()
	var doc sourceReleaseDocument
	if m.sourceReleases != nil {
		doc = m.sourceReleases[source]
	}
	m.mu.Unlock()
	if doc.Version != version || doc.Source != source || !newerVersion(doc.Version, Version) {
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
		fmt.Fprintf(log, "Preparing Pilot GitHub checksum update to %s [%s]\n", version, source)
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
		if e = m.prepareUpdateFrom(ctx, job, releaseDocument{Version: doc.Version, ManifestSHA256: doc.ManifestSHA256, EngineVersion: doc.EngineVersion, EngineSHA256: doc.EngineSHA256}, sourceReleaseBase); e != nil {
			m.log.Add("Обновление не подготовлено: " + e.Error())
			fail("prepare", e)
			return
		}
		worker, e := os.ReadFile(filepath.Join(filepath.Dir(m.Config().EngineDir), "scripts/source-update-worker.sh"))
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


