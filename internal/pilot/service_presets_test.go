package pilot

import (
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestServiceCatalogProfilesAndIsolation(t *testing.T) {
	c := DefaultConfig()
	c.Profiles = nil
	seen := map[string]bool{}
	for _, s := range ServicePresets() {
		if seen[s.Profile.ID] || s.Profile.Strategy != "none" || s.Note == "" {
			t.Fatal("invalid preset", s.Profile.ID)
		}
		seen[s.Profile.ID] = true
		c.Profiles = append(c.Profiles, s.Profile)
	}
	if len(seen) != 9 {
		t.Fatalf("expected nine services including Telegram Web, got %d", len(seen))
	}
	if err := c.Validate(); err != nil {
		t.Fatal("combined presets invalid", err)
	}
	clone := ServicePresets()
	clone[0].Profile.Domains[0] = "changed.test"
	clone[0].Profile.Targets[0].Contains = "changed"
	if ServicePresets()[0].Profile.Domains[0] == "changed.test" || ServicePresets()[0].Profile.Targets[0].Contains == "changed" {
		t.Fatal("preset data shared with caller")
	}
}
func TestServiceAddRemovePreservesConfigurationAndChecksConflicts(t *testing.T) {
	c := DefaultConfig()
	c.Profiles[0].Strategy = "fake-seq"
	c.DomainPins = []DomainPin{{Host: "www.youtube.com", Strategy: "fake-md5", TLS: "any", Family: "any", Port: 0}}
	c.PausedDomains = []string{"paused.test"}
	path := filepath.Join(t.TempDir(), "config.json")
	m := NewManager(c, path, &testBackend{}, successfulProber{}, &LogBuffer{})
	if err := m.AddService("instagram"); err != nil {
		t.Fatal(err)
	}
	if got := m.Config(); len(got.Profiles) != 3 || got.Profiles[0].Strategy != "fake-seq" || got.Profiles[2].Strategy != "none" || len(got.DomainPins) != 1 || len(got.PausedDomains) != 1 {
		t.Fatal("existing configuration changed")
	}
	if m.AddService("instagram") == nil || m.AddService("missing") == nil {
		t.Fatal("duplicate/unknown service accepted")
	}
	if err := m.RemoveService("instagram"); err != nil {
		t.Fatal(err)
	}
	if got := m.Config(); len(got.Profiles) != 2 || got.Profiles[0].Strategy != "fake-seq" || len(got.DomainPins) != 1 {
		t.Fatal("remove lost existing settings")
	}
	conflict := m.Config()
	conflict.Profiles[0].Domains = append(conflict.Profiles[0].Domains, "instagram.com")
	if err := m.Change(conflict); err != nil {
		t.Fatal(err)
	}
	if m.AddService("instagram") == nil || len(m.Config().Profiles) != 2 {
		t.Fatal("conflicting domains accepted")
	}
	m.op.Lock()
	err := m.AddService("signal")
	m.op.Unlock()
	if err != ErrBusy {
		t.Fatal("operation lock ignored")
	}
	if err := m.RemoveService("discord"); err != nil {
		t.Fatal(err)
	}
	if m.RemoveService("youtube") == nil {
		t.Fatal("last profile removed")
	}
	saved, err := LoadConfig(path)
	if err != nil || len(saved.Profiles) != 1 {
		t.Fatal("profile changes not persisted", err)
	}
}
func TestServiceEndpointsAuthAndValidation(t *testing.T) {
	m := NewManager(DefaultConfig(), filepath.Join(t.TempDir(), "config.json"), &testBackend{}, successfulProber{}, &LogBuffer{})
	token := strings.Repeat("s", 64)
	h := Handler(m, token)
	call := func(method, path, auth, origin, body string) int {
		r := httptest.NewRequest(method, "http://router.test"+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", auth)
		r.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	if call("POST", "/api/services", "", "", `{"id":"signal"}`) != 401 || call("POST", "/api/services", "Bearer "+token, "http://evil.test", `{"id":"signal"}`) != 403 {
		t.Fatal("unprotected preset mutation")
	}
	if call("POST", "/api/services", "Bearer "+token, "", `{"id":"signal"}`) != 200 || call("DELETE", "/api/services/signal", "Bearer "+token, "", "") != 200 {
		t.Fatal("valid service change rejected")
	}
	if call("POST", "/api/services", "Bearer "+token, "", `{"id":"signal","domains":["evil.test"]}`) != 400 {
		t.Fatal("client supplied preset contents accepted")
	}
}
