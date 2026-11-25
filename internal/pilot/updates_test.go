package pilot

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type updateRouterBackend struct{ testBackend }

func (b *updateRouterBackend) Kind() string { return "router" }

type updateTransport func(*http.Request) (*http.Response, error)

func (f updateTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func packageTar(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	for name, data := range files {
		if e := tw.WriteHeader(&tar.Header{Name: name, Mode: 0755, Size: int64(len(data)), Typeflag: tar.TypeReg}); e != nil {
			t.Fatal(e)
		}
		tw.Write([]byte(data))
	}
	tw.Close()
	gz.Close()
	return b.Bytes()
}
func updateFixture(t *testing.T) (*Manager, string) {
	t.Helper()
	prefix := filepath.Join(t.TempDir(), "pilot")
	app := filepath.Join(prefix, "keenetic-pilot")
	dir := filepath.Join(prefix, "etc/keenetic-pilot")
	os.MkdirAll(filepath.Join(app, "scripts"), 0700)
	os.MkdirAll(filepath.Join(prefix, "etc/init.d"), 0700)
	os.MkdirAll(dir, 0700)
	os.MkdirAll(filepath.Join(prefix, "var/run"), 0700)
	os.WriteFile(filepath.Join(prefix, "var/run/keenetic-pilot.pid"), []byte(fmt.Sprint(os.Getpid())), 0600)
	os.WriteFile(filepath.Join(app, ".pilot-managed"), []byte("keenetic-pilot\n"), 0600)
	os.WriteFile(filepath.Join(prefix, "etc/init.d/S98keenetic-pilot"), []byte("#!/bin/sh\n# Keenetic Pilot\nexit 0\n"), 0700)
	worker, e := os.ReadFile("../../scripts/update-worker.sh")
	if e != nil {
		t.Fatal(e)
	}
	os.WriteFile(filepath.Join(app, "scripts/update-worker.sh"), worker, 0700)
	c := DefaultConfig()
	c.EngineDir = filepath.Join(app, "engine")
	c.RuntimeDir = filepath.Join(prefix, "keenetic-pilot-state")
	path := filepath.Join(dir, "config.json")
	if e := WriteJSON(path, c); e != nil {
		t.Fatal(e)
	}
	return NewManager(c, path, &updateRouterBackend{}, successfulProber{}, &LogBuffer{}), prefix
}
func TestReleaseSignatureComparisonAndSafeExtraction(t *testing.T) {
	if !newerVersion("0.2.0-alpha.10", "0.2.0-alpha.9") || newerVersion("0.2.0-alpha.9", "0.2.0-alpha.10") || newerVersion("../evil", "0.2.0-alpha.9") {
		t.Fatal("bad version ordering")
	}
	for _, name := range []string{"../outside", "keenetic-pilot/../../outside", "/absolute", "keenetic-pilot/./file"} {
		if extractUpdate(bytes.NewReader(packageTar(t, map[string]string{name: "evil"})), t.TempDir()) == nil {
			t.Fatal("unsafe archive path accepted", name)
		}
	}
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "keenetic-pilot/link", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"})
	tw.Close()
	gz.Close()
	if extractUpdate(bytes.NewReader(b.Bytes()), t.TempDir()) == nil {
		t.Fatal("archive symlink accepted")
	}
	dir := t.TempDir()
	if e := extractUpdate(bytes.NewReader(packageTar(t, map[string]string{"keenetic-pilot/file": "verified"})), dir); e != nil {
		t.Fatal(e)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "keenetic-pilot/file"))
	if string(got) != "verified" {
		t.Fatal("archive contents lost")
	}
}
func TestSignedMenuUpdateWorkerLifecycleAndChecksumFailure(t *testing.T) {
	oldVersion, oldKey, oldTransport := Version, updatesPublicKey, http.DefaultTransport
	defer func() { Version = oldVersion; updatesPublicKey = oldKey; http.DefaultTransport = oldTransport }()
	Version = "0.2.0-alpha.9"
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	updatesPublicKey = base64.StdEncoding.EncodeToString(pub)
	m, prefix := updateFixture(t)
	expectedEngine := strings.Repeat("a", 64)
	files := map[string]string{
		"keenetic-pilot/PLATFORM": runtime.GOARCH + "\n", "keenetic-pilot/VERSION": "0.2.0-alpha.10\n",
		"keenetic-pilot/dependencies/nfqws2.env": "ENGINE_VERSION=v-test\nENGINE_SHA256=" + expectedEngine + "\n",
		"keenetic-pilot/bin/pilot":               "#!/bin/sh\nif [ \"$1\" = --version ]; then echo 0.2.0-alpha.10; fi\nexit 0\n",
		"keenetic-pilot/scripts/install.sh":      "#!/bin/sh\nBASE=$(dirname \"$0\")/..\nmkdir -p \"$2/keenetic-pilot/bin\"\ncp \"$BASE/bin/pilot\" \"$2/keenetic-pilot/bin/pilot\"\ntouch \"$2/update-worker-complete\"\n",
	}
	archive := packageTar(t, files)
	archiveHash := sha256.Sum256(archive)
	name := "keenetic-pilot-0.2.0-alpha.10-linux-" + runtime.GOARCH + ".tar.gz"
	manifest := []byte(hex.EncodeToString(archiveHash[:]) + "  " + name + "\n")
	manifestHash := sha256.Sum256(manifest)
	doc := releaseDocument{Kind: "pilot-release", Version: "0.2.0-alpha.10", ManifestSHA256: hex.EncodeToString(manifestHash[:]), PublishedAt: time.Now().UTC(), EngineVersion: "v-test", EngineSHA256: expectedEngine}
	data, _ := json.Marshal(doc)
	sig := []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(priv, data)))
	http.DefaultTransport = updateTransport(func(r *http.Request) (*http.Response, error) {
		body := []byte{}
		status := 200
		switch r.URL.Path {
		case "/punisher89/my.kn-test-/refs/heads/packages/latest.json":
			body = data
		case "/punisher89/my.kn-test-/refs/heads/packages/latest.json.sig":
			body = sig
		default:
			if strings.HasSuffix(r.URL.Path, "SHA256SUMS") {
				body = manifest
			} else if strings.HasSuffix(r.URL.Path, ".tar.gz") {
				body = archive
			} else {
				status = 404
			}
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(bytes.NewReader(body)), Header: make(http.Header)}, nil
	})
	m.op.Lock()
	checkErr := m.CheckUpdate(t.Context())
	m.op.Unlock()
	if checkErr != nil {
		t.Fatal("version check was blocked by active search", checkErr)
	}
	if !m.Updates().Available || !m.Updates().Supported {
		t.Fatal("verified release not offered")
	}
	if m.StartUpdate("0.2.0-alpha.11") == nil {
		t.Fatal("unreviewed version accepted")
	}
	bad := doc
	bad.ManifestSHA256 = strings.Repeat("0", 64)
	if m.prepareUpdate(t.Context(), t.TempDir(), bad) == nil {
		t.Fatal("bad manifest hash accepted")
	}
	original, _ := os.ReadFile(m.path)
	os.WriteFile(filepath.Join(filepath.Dir(m.path), "password.json"), []byte("private-password-fixture"), 0600)
	// Model an active search holding the operation lock. Installation must
	// request its cancellation and wait for cleanup before running the worker.
	m.op.Lock()
	searchCtx, cancelSearch := context.WithCancel(context.Background())
	m.mu.Lock()
	m.cancel = cancelSearch
	m.mu.Unlock()
	cleanupReady := make(chan struct{})
	cleanupDone := make(chan struct{})
	go func() {
		<-searchCtx.Done()
		close(cleanupReady)
		<-cleanupDone
		m.mu.Lock()
		m.cancel = nil
		m.mu.Unlock()
		m.op.Unlock()
	}()
	if e := m.StartUpdate(doc.Version); e != nil {
		t.Fatal(e)
	}
	select {
	case <-cleanupReady:
	case <-time.After(time.Second):
		close(cleanupDone)
		t.Fatal("active search was not cancelled for update")
	}
	if status := m.Updates(); !status.Running || status.Stage != "waiting" {
		close(cleanupDone)
		t.Fatal("waiting for cleanup not shown", status)
	}
	if _, err := os.Stat(filepath.Join(prefix, "update-worker-complete")); !os.IsNotExist(err) {
		close(cleanupDone)
		t.Fatal("installer ran before search cleanup")
	}
	if err := m.StartUpdate(doc.Version); !errors.Is(err, ErrBusy) {
		close(cleanupDone)
		t.Fatal("duplicate update was accepted", err)
	}
	close(cleanupDone)
	deadline := time.Now().Add(5 * time.Second)
	for m.updateRequested.Load() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if m.updateRequested.Load() {
		t.Fatal("detached update worker did not complete")
	}
	status := m.Updates()
	if status.Running || status.Stage != "completed" {
		t.Fatalf("bad update status: %+v", status)
	}
	if _, e := os.Stat(filepath.Join(prefix, "update-worker-complete")); e != nil {
		t.Fatal("worker never ran installer")
	}
	after, _ := os.ReadFile(m.path)
	if !bytes.Equal(original, after) {
		t.Fatal("worker fixture modified config")
	}
	password, _ := os.ReadFile(filepath.Join(filepath.Dir(m.path), "password.json"))
	if string(password) != "private-password-fixture" {
		t.Fatal("password changed")
	}
	if status.Job != "" || status.PID != 0 {
		t.Fatal("private update paths exposed")
	}
	// A controller can shut down while the updater owns the operation lock.
	m.op.Lock()
	m.updating.Store(true)
	done := make(chan struct{})
	go func() { m.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("update deadlocked controller shutdown")
	}
	m.updating.Store(false)
	m.op.Unlock()
}

func TestUpdateWorkerRecordsInstallerFailureAndExitCode(t *testing.T) {
	m, prefix := updateFixture(t)
	job, err := os.MkdirTemp(filepath.Dir(m.path), "update-job-")
	if err != nil {
		t.Fatal(err)
	}
	scripts := filepath.Join(job, "keenetic-pilot/scripts")
	if err := os.MkdirAll(scripts, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(scripts, "install.sh"), []byte("#!/bin/sh\necho 'fixture disk write failed' >&2\nexit 23\n"), 0700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", "../../scripts/update-worker.sh", job, prefix, "0.2.0-alpha.15")
	if err := cmd.Run(); err == nil {
		t.Fatal("failed installer reported success")
	}
	status := m.Updates()
	if status.Stage != "failed" || status.Phase != "install" || status.ExitCode != 23 || status.Running {
		t.Fatalf("failure details missing: %+v", status)
	}
	log, err := os.ReadFile(filepath.Join(filepath.Dir(m.path), "update.log"))
	if err != nil || !bytes.Contains(log, []byte("fixture disk write failed")) || !bytes.Contains(log, []byte("exit 23")) {
		t.Fatal("durable failure log missing", err, string(log))
	}
	if _, err := os.Stat(job); !os.IsNotExist(err) {
		t.Fatal("worker left extracted update behind", err)
	}
}

func TestUpdatePreparationFailureIsWrittenToDurableLog(t *testing.T) {
	oldVersion, oldTransport := Version, http.DefaultTransport
	defer func() { Version = oldVersion; http.DefaultTransport = oldTransport }()
	Version = "0.2.0-alpha.14"
	m, _ := updateFixture(t)
	m.release = &releaseDocument{Version: "0.2.0-alpha.15", ManifestSHA256: strings.Repeat("a", 64)}
	http.DefaultTransport = updateTransport(func(_ *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("fixture offline")
	})
	if err := m.StartUpdate("0.2.0-alpha.15"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for m.updateRequested.Load() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if m.updateRequested.Load() {
		t.Fatal("failed download did not release update lock")
	}
	status := m.Updates()
	if status.Stage != "failed" || status.Phase != "prepare" || status.Running {
		t.Fatalf("preparation failure details missing: %+v", status)
	}
	log, err := os.ReadFile(filepath.Join(filepath.Dir(m.path), "update.log"))
	if err != nil || !bytes.Contains(log, []byte("fixture offline")) {
		t.Fatal("preparation error not recorded", err)
	}
}

func TestNewMenuEndpointsRequireAuthorization(t *testing.T) {
	m := NewManager(DefaultConfig(), filepath.Join(t.TempDir(), "config.json"), &testBackend{}, successfulProber{}, &LogBuffer{})
	token := strings.Repeat("a", 64)
	h := Handler(m, token)
	for _, route := range []struct{ method, path string }{{"GET", "/api/device"}, {"GET", "/api/updates"}, {"POST", "/api/updates/check"}, {"POST", "/api/updates/install"}, {"POST", "/api/services/custom"}, {"POST", "/api/services/youtube/domains"}} {
		req := httptest.NewRequest(route.method, "http://router.test"+route.path, strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != 401 {
			t.Fatal("unprotected endpoint", route.path, w.Code)
		}
		req = httptest.NewRequest(route.method, "http://router.test"+route.path, strings.NewReader(`{}`))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Origin", "http://evil.test")
		w = httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != 403 {
			t.Fatal("foreign origin allowed", route.path, w.Code)
		}
	}
}

func TestNativeUpdatePackagePreflight(t *testing.T) {
	path := os.Getenv("PILOT_TEST_PACKAGE")
	if path == "" {
		t.Skip("built native package required")
	}
	archive, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	v, e := os.ReadFile("../../VERSION")
	if e != nil {
		t.Fatal(e)
	}
	version := strings.TrimSpace(string(v))
	name := "keenetic-pilot-" + version + "-linux-" + runtime.GOARCH + ".tar.gz"
	sum := sha256.Sum256(archive)
	manifest := []byte(hex.EncodeToString(sum[:]) + "  " + name + "\n")
	manifestSum := sha256.Sum256(manifest)
	dep, e := os.ReadFile("../../dependencies/nfqws2.env")
	if e != nil {
		t.Fatal(e)
	}
	fields := map[string]string{}
	for _, line := range strings.Split(string(dep), "\n") {
		p := strings.SplitN(line, "=", 2)
		if len(p) == 2 {
			fields[p[0]] = p[1]
		}
	}
	doc := releaseDocument{Kind: "pilot-release", Version: version, ManifestSHA256: hex.EncodeToString(manifestSum[:]), EngineVersion: fields["ENGINE_VERSION"], EngineSHA256: fields["ENGINE_SHA256"], PublishedAt: time.Now()}
	previous := http.DefaultTransport
	defer func() { http.DefaultTransport = previous }()
	http.DefaultTransport = updateTransport(func(r *http.Request) (*http.Response, error) {
		data := archive
		if strings.HasSuffix(r.URL.Path, "SHA256SUMS") {
			data = manifest
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(data)), Header: make(http.Header)}, nil
	})
	m, _ := updateFixture(t)
	if e = m.prepareUpdate(t.Context(), t.TempDir(), doc); e != nil {
		t.Fatal("real native package preflight failed", e)
	}
}

func TestBetaVersionOrdering(t *testing.T) {
 cases := []struct{a,b string; want bool}{
  {"0.2.0-beta.1","0.2.0-alpha.999",true},
  {"0.2.0-alpha.999","0.2.0-beta.1",false},
  {"0.2.0-beta.10","0.2.0-beta.9",true},
  {"0.2.0-beta.1","0.2.0-beta.1",false},
  {"0.3.0-alpha.1","0.2.0-beta.999",true},
  {"0.2.0-beta.4294967296","0.2.0-alpha.1",false},
  {"0.2.0-beta.1/../x","0.2.0-alpha.1",false},
  {"0.2.0-rc.1","0.2.0-alpha.1",false},
 }
 for _,tc:=range cases {
  if got:=newerVersion(tc.a,tc.b);got!=tc.want {t.Errorf("%s > %s: %v, want %v",tc.a,tc.b,got,tc.want)}
 }
}
