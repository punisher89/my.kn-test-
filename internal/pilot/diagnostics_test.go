package pilot

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDiagnosticsAllowlistAndReadOnlyExport(t *testing.T) {
	dir := t.TempDir()
	c := DefaultConfig()
	c.Adaptive = true
	c.Enabled = true
	c.WAN = "ppp0"
	c.RuntimeDir = filepath.Join(dir, "private-path-secret")
	if err := os.Mkdir(c.RuntimeDir, 0700); err != nil {
		t.Fatal(err)
	}
	c.Profiles[0].Name = "profile-name-secret"
	c.Profiles[0].Targets = []Target{{URL: "https://user:probe-password-secret@example.com/?token=probe-query-secret", Contains: "probe-body-secret"}}
	c.Catalog = []Strategy{{ID: "net-test", Name: "catalog-name-secret", Description: "catalog-description-secret", Actions: []string{"multisplit:pos=1,midsld"}}}
	c.DomainPins = []DomainPin{{Host: "pinned.example.com", Strategy: "split-sni", TLS: "tls13", Family: "ipv4", Port: 443}}
	c.PausedDomains = []string{"paused.example.com"}
	c.Exclusions = []string{"excluded.test"}
	for _, name := range []string{"password.json", "github-token", "api-key", "pairing.json"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("credential-file-secret"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().Unix()
	row := AdaptiveHost{Group: "discovered_tls13_ipv4", Host: "example.com", Strategy: "fake-md5", Status: "probing", Updated: now - 1, Symptom: "rst", Network: "ppp0", Port: 443, Origin: "pool", LastSwitch: now - 10, WindowStart: now - 100, Switches: 2, HoldUntil: now + 300, HoldReason: "cooldown"}
	current := []AdaptiveHost{row, row, row, row}
	current[1].Host = "pinned.example.com"
	current[2].Host = "paused.example.com"
	current[3].Host = "unknown.example.com"
	current[3].Strategy = "removed-method"
	current[3].HoldUntil = now - 1
	event := row
	event.Status = "held"
	event.HoldUntil = now - 30
	valid := adaptiveFrame(4, current, []AdaptiveHost{event})
	invalid := []byte("damaged-slot-secret")
	if err := os.WriteFile(filepath.Join(c.RuntimeDir, "state1.tsv"), valid, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(c.RuntimeDir, "state0.tsv"), invalid, 0600); err != nil {
		t.Fatal(err)
	}
	discovered := []byte("example.com\n")
	if err := os.WriteFile(filepath.Join(c.RuntimeDir, "autohosts.txt"), discovered, 0600); err != nil {
		t.Fatal(err)
	}
	backend := &testBackend{running: true}
	log := &LogBuffer{}
	log.Add("log-line-secret")
	m := NewManager(c, filepath.Join(dir, "config.json"), backend, successfulProber{}, log)
	m.lastError = "engine-error-secret"
	m.job = &Job{Profile: "job-profile-secret", Error: "job-error-secret", Trials: []Trial{{Error: "trial-error-secret", Results: []ProbeResult{{URL: "https://probe-result-secret.test"}}}}}
	token := strings.Repeat("bearer-token-secret", 4)
	h := Handler(m, token)
	req := httptest.NewRequest("GET", "http://router.test/api/diagnostics", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("export failed: %d %s", rec.Code, rec.Body.String())
	}
	for _, secret := range []string{"private-path-secret", "profile-name-secret", "probe-password-secret", "probe-query-secret", "probe-body-secret", "catalog-name-secret", "catalog-description-secret", "credential-file-secret", "damaged-slot-secret", "log-line-secret", "engine-error-secret", "job-profile-secret", "job-error-secret", "trial-error-secret", "probe-result-secret", "bearer-token-secret"} {
		if strings.Contains(rec.Body.String(), secret) {
			t.Errorf("export leaked %s", secret)
		}
	}
	if rec.Header().Get("Cache-Control") != "no-store" || !strings.Contains(rec.Header().Get("Content-Disposition"), "attachment; filename=\"keenetic-pilot-diagnostics-") || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
		t.Fatal("missing download/cache headers")
	}
	var report DiagnosticReport
	if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.SchemaVersion != 1 || report.GeneratedAt.IsZero() || !report.Service.HasError || !report.Service.Running || report.Generation != 4 || len(report.Current) != 4 || len(report.History) != 1 || len(report.Discovered) != 1 {
		t.Fatalf("incomplete report: %+v", report)
	}
	if report.Current[0].Reason != "cooldown" || report.Current[1].Reason != "pinned_pending" || report.Current[2].Reason != "paused" || report.Current[3].Reason != "probing" || report.Current[3].StrategyKnown {
		t.Fatal("wrong effective reasons or missing method detection")
	}
	if report.History[0].Reason != "cooldown" || report.History[0].HoldUntil >= now {
		t.Fatal("expired history was rewritten")
	}
	if len(report.Rules.Pins) != 1 || report.Rules.Pins[0].Strategy != "split-sni" || len(report.Rules.Exclusions) != 1 || len(report.Rules.Paused) != 1 {
		t.Fatal("manual rules lost")
	}
	if len(report.Storage) != 2 || report.Storage[0].State != "invalid_or_incomplete" || report.Storage[1].State != "valid" {
		t.Fatal("storage problems hidden")
	}
	for name, want := range map[string][]byte{"state0.tsv": invalid, "state1.tsv": valid, "autohosts.txt": discovered} {
		got, err := os.ReadFile(filepath.Join(c.RuntimeDir, name))
		if err != nil || string(got) != string(want) {
			t.Fatalf("export modified %s", name)
		}
	}
	if len(backend.configs) != 0 || !backend.Running() {
		t.Fatal("export changed service state")
	}
}

func TestDiagnosticsAuthOriginAndEmptyState(t *testing.T) {
	dir := t.TempDir()
	c := DefaultConfig()
	c.RuntimeDir = filepath.Join(dir, "missing")
	m := NewManager(c, filepath.Join(dir, "config.json"), &testBackend{}, successfulProber{}, &LogBuffer{})
	token := strings.Repeat("t", 64)
	h := Handler(m, token, NewAuth(dir))
	call := func(auth, origin string, cookie *http.Cookie) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "http://router.test/api/diagnostics", nil)
		req.Header.Set("Authorization", auth)
		req.Header.Set("Origin", origin)
		if cookie != nil {
			req.AddCookie(cookie)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	if call("", "", nil).Code != 401 || call("Bearer "+token, "http://evil.test", nil).Code != 403 {
		t.Fatal("export is not protected")
	}
	code, err := Pair(dir)
	if err != nil {
		t.Fatal(err)
	}
	setup := authCall(h, "/auth/setup", fmt.Sprintf(`{"code":%q,"password":"test-password-long"}`, code), nil)
	if setup.Code != 200 {
		t.Fatalf("setup failed: %d", setup.Code)
	}
	rec := call("", "http://router.test", setup.Result().Cookies()[0])
	if rec.Code != 200 {
		t.Fatalf("session export failed: %d", rec.Code)
	}
	var report DiagnosticReport
	if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Current == nil || report.History == nil || report.Discovered == nil || len(report.Current) != 0 || report.Storage[0].State != "missing" || report.Storage[1].State != "missing" {
		t.Fatal("missing state must be explicit, with empty arrays")
	}
	if err := os.Mkdir(c.RuntimeDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(c.RuntimeDir, "state0.tsv"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(c.RuntimeDir, "state1.tsv"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	empty := m.Diagnostics()
	if empty.Storage[0].State != "unreadable" || empty.Storage[1].State != "invalid_or_incomplete" {
		t.Fatal("unreadable and empty storage must be distinguishable")
	}
}

func TestDiagnosticReasonsUseCurrentContextOnly(t *testing.T) {
	row := AdaptiveHost{Group: "discovered_tls13_ipv4", Host: "example.com", Strategy: "fake-md5", Status: "probing", HoldReason: "limit", HoldUntil: 200}
	s := Snapshot{Running: true, Config: DefaultConfig()}
	s.Config.Adaptive = true
	for _, test := range []struct {
		name              string
		row               AdaptiveHost
		running, adaptive bool
		want              string
	}{
		{"active limit", row, true, true, "limit"},
		{"expired", row, true, true, "probing"},
		{"stopped", row, false, true, "service_stopped"},
		{"disabled", row, true, false, "adaptive_disabled"},
		{"pinned", func() AdaptiveHost { r := row; r.Pinned = r.Strategy; return r }(), true, true, "pinned"},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := s
			state.Running = test.running
			state.Config.Adaptive = test.adaptive
			now := int64(100)
			if test.name == "expired" {
				now = 200
			}
			if got := diagnosticObservation(test.row, state, now, false); got.Reason != test.want {
				t.Fatalf("got %s, want %s", got.Reason, test.want)
			}
		})
	}
	history := row
	history.Status = "held"
	history.Pinned = "split-sni"
	history.Paused = true
	got := diagnosticObservation(history, s, 300, true)
	if got.Reason != "limit" || got.Paused || got.Pinned != "" {
		t.Fatal("current overrides contaminated history")
	}
}
