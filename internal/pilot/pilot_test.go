package pilot

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestConfigurationRejectsUnsafeAndAmbiguousTargets(t *testing.T) {
	if err := DefaultConfig().Validate(); err != nil {
		t.Fatal(err)
	}
	for name, edit := range map[string]func(*Config){
		"public-listener":     func(c *Config) { c.Listen = "0.0.0.0:9080" },
		"shell-interface":     func(c *Config) { c.WAN = "ppp0;reboot" },
		"overlapping-domains": func(c *Config) { c.Profiles[1].Domains = append(c.Profiles[1].Domains, "www.youtube.com") },
		"local-target":        func(c *Config) { c.Profiles[0].Targets[0].URL = "https://192.168.1.1/" },
		"uncovered-target":    func(c *Config) { c.Profiles[0].Targets[0].URL = "https://example.com/" },
		"excluded-probe":      func(c *Config) { c.Exclusions = []string{"youtube.com"} },
	} {
		t.Run(name, func(t *testing.T) {
			c := DefaultConfig()
			edit(&c)
			if c.Validate() == nil {
				t.Fatal("configuration accepted")
			}
		})
	}
}

func TestHTTPSChecksContentAndNeverFollowsRedirect(t *testing.T) {
	for _, version := range []uint16{tls.VersionTLS12, tls.VersionTLS13} {
		t.Run(map[uint16]string{tls.VersionTLS12: "TLS12", tls.VersionTLS13: "TLS13"}[version], func(t *testing.T) {
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/redirect" {
					http.Redirect(w, r, "/", 302)
					return
				}
				_, _ = io.WriteString(w, "control-marker")
			}))
			server.TLS = &tls.Config{MinVersion: version, MaxVersion: version}
			server.StartTLS()
			defer server.Close()
			transport := server.Client().Transport.(*http.Transport).Clone()
			transport.TLSClientConfig.MinVersion = version
			transport.TLSClientConfig.MaxVersion = version
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if _, err := performRequest(ctx, transport, Target{URL: server.URL, Status: 200, Contains: "control-marker"}); err != nil {
				t.Fatal(err)
			}
			if _, err := performRequest(ctx, transport, Target{URL: server.URL, Status: 200, Contains: "missing"}); err == nil {
				t.Fatal("wrong content passed")
			}
			if _, err := performRequest(ctx, transport, Target{URL: server.URL + "/redirect", Status: 200}); err == nil {
				t.Fatal("redirect followed")
			}
		})
	}
}

func TestPrivateProbeAddressesAreRejected(t *testing.T) {
	r := HTTPProber{}.probe(context.Background(), Target{URL: "https://localhost/", Status: 200}, "tcp4", tls.VersionTLS12, time.Second, false)
	if r.OK || !strings.Contains(r.Error, "непубличный") {
		t.Fatalf("unexpected result: %+v", r)
	}
}

func TestAPIRequiresTokenAndRejectsCrossOrigin(t *testing.T) {
	log := &LogBuffer{}
	m := NewManager(DefaultConfig(), filepath.Join(t.TempDir(), "config.json"), Development{}, HTTPProber{}, log)
	token := strings.Repeat("a", 64)
	h := Handler(m, token)
	for _, item := range []struct {
		auth, origin string
		code         int
	}{{"", "", 401}, {"Bearer " + token, "http://evil.test", 403}, {"Bearer " + token, "", 200}} {
		req := httptest.NewRequest("GET", "http://router.test/api/status", nil)
		req.Header.Set("Authorization", item.auth)
		req.Header.Set("Origin", item.origin)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != item.code {
			t.Fatalf("got %d want %d: %s", rr.Code, item.code, rr.Body.String())
		}
	}
	if err := m.SetEnabled(true); err == nil || m.Config().Enabled {
		t.Fatal("development mode pretends to run bypass")
	}
}

type testBackend struct {
	mu      sync.Mutex
	running bool
	configs []Config
}

func (b *testBackend) Start(c Config) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.running = true
	b.configs = append(b.configs, cloneConfig(c))
	return nil
}
func (b *testBackend) Stop() error   { b.mu.Lock(); defer b.mu.Unlock(); b.running = false; return nil }
func (b *testBackend) Running() bool { b.mu.Lock(); defer b.mu.Unlock(); return b.running }
func (b *testBackend) Repair() error { return nil }
func (b *testBackend) Kind() string  { return "test" }

type failedProber struct{}

func (failedProber) Check(_ context.Context, p Profile, c Config, direct bool) []ProbeResult {
	return []ProbeResult{{URL: p.Targets[0].URL, OK: false}}
}

func TestFailedTuningRestoresPreviousConfig(t *testing.T) {
	c := DefaultConfig()
	c.Enabled = true
	c.WAN = "ppp0"
	path := filepath.Join(t.TempDir(), "config.json")
	if err := WriteJSON(path, c); err != nil {
		t.Fatal(err)
	}
	b := &testBackend{running: true}
	m := NewManager(c, path, b, failedProber{}, &LogBuffer{})
	if err := m.BeginTune("youtube"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		snapshot := m.Snapshot()
		if snapshot.Job != nil && snapshot.Job.Finished != nil && !snapshot.Busy {
			break
		}
		time.Sleep(time.Millisecond)
	}
	s := m.Snapshot()
	if s.Job == nil || s.Job.Finished == nil || s.Job.Selected != "" || s.Job.Stage != "failed" {
		t.Fatalf("unexpected job: %+v", s.Job)
	}
	saved, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Profiles[0].Strategy != c.Profiles[0].Strategy {
		t.Fatal("failed strategy persisted")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.configs) == 0 || b.configs[len(b.configs)-1].Profiles[0].Strategy != c.Profiles[0].Strategy {
		t.Fatal("previous engine configuration not restored")
	}
}

func TestGeneratedStrategiesWithRealNfqws(t *testing.T) {
	dir := os.Getenv("PILOT_TEST_ENGINE")
	if dir == "" {
		t.Skip("set PILOT_TEST_ENGINE to unpacked engine directory for native --dry-run validation")
	}
	for _, strategy := range Strategies {
		t.Run(strategy.ID, func(t *testing.T) {
			c := DefaultConfig()
			c.EngineDir = dir
			c.Profiles[0].Strategy = strategy.ID
			c.DiscordVoice = true
			c.TelegramMTProto = true // Native engine must parse the opt-in MTProto profile.
			c.IPv6 = true
			args := append([]string{"--dry-run"}, EngineArgs(c)...)
			cmd := exec.Command(filepath.Join(dir, "nfqws2"), args...)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("%v\n%s\nargs: %q", err, out, args)
			}
		})
	}
}

func TestJSONWritePreservesConfigAndPermissions(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	c := DefaultConfig()
	if err := WriteJSON(p, c); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var got Config
	if err = json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if err = got.Validate(); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(p)
	if info.Mode().Perm() != 0600 {
		t.Fatalf("permissions: %o", info.Mode().Perm())
	}
}
