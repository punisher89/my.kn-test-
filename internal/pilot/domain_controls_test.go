package pilot

import (
	"bytes"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDomainPinAndPauseNativeArgumentsWithAdaptiveOff(t *testing.T) {
	c := adaptiveFixture(t)
	c.Adaptive = false
	c.DomainPins = []DomainPin{{Host: "www.youtube.com", Strategy: "fake-md5", TLS: "tls12", Family: "ipv4", Port: 443}}
	c.PausedDomains = []string{"discord.com"}
	if err := CheckEngine(c); err != nil {
		t.Fatal(err)
	}
	args := strings.Join(EngineArgs(c), " ")
	if !strings.Contains(args, "pilot_adaptive=false") || !strings.Contains(args, "pilot_pins=") || !strings.Contains(args, "--hostlist-exclude-domains=discord.com") {
		t.Fatal("manual controls missing with auto mode off")
	}
	c.PausedDomains = nil
	c.DomainPins = append(c.DomainPins, DomainPin{Host: "www.youtube.com", Strategy: "disorder", TLS: "any", Family: "ipv4", Port: 443})
	if c.Validate() == nil {
		t.Fatal("ambiguous overlapping pins accepted")
	}
}

func TestPinnedMethodsArePreservedWhenCatalogChanges(t *testing.T) {
	c := adaptiveFixture(t)
	c.CatalogRevision = 1
	c.Catalog = []Strategy{{ID: "net-kept", Name: "Kept", Description: "Test", Actions: []string{"multisplit:pos=2,midsld"}}}
	c.DomainPins = []DomainPin{{Host: "example.com", Strategy: "net-kept", TLS: "tls12", Family: "ipv4", Port: 443}}
	m := NewManager(c, filepath.Join(t.TempDir(), "config.json"), &testBackend{}, successfulProber{}, &LogBuffer{})
	err := m.acceptCatalog(t.Context(), catalogDocument{Revision: 2, Strategies: []Strategy{{ID: "net-new", Name: "New", Description: "Test", Actions: []string{"multisplit:pos=3,midsld"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := strategyFor(m.Config(), "net-kept"); !ok {
		t.Fatal("catalog removed a pinned method")
	}
}

func TestDomainEndpointAuthValidationAndProfileTuneProtection(t *testing.T) {
	c := DefaultConfig()
	path := filepath.Join(t.TempDir(), "config.json")
	m := NewManager(c, path, &testBackend{}, successfulProber{}, &LogBuffer{})
	token := strings.Repeat("t", 64)
	handler := Handler(m, token)
	body := `{"host":"www.youtube.com","action":"pin","strategy":"fake-md5","tls":"tls12","family":"ipv4","port":443}`
	call := func(auth, origin, body string) int {
		req := httptest.NewRequest("POST", "http://router.test/api/adaptive/domain", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", auth)
		req.Header.Set("Origin", origin)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Code
	}
	if call("", "", body) != 401 || call("Bearer "+token, "http://evil.test", body) != 403 {
		t.Fatal("domain controls lacked authentication/origin checks")
	}
	if call("Bearer "+token, "", body) != 200 {
		t.Fatal("valid pin failed")
	}
	if profileControlReason(m.Config(), m.Config().Profiles[0]) == "" {
		t.Fatal("profile tuner would test across a manual pin")
	}
	if call("Bearer "+token, "", `{"host":"../private","action":"reset"}`) != 400 {
		t.Fatal("unsafe hostname accepted")
	}
	m.op.Lock()
	code := call("Bearer "+token, "", body)
	m.op.Unlock()
	if code != 409 {
		t.Fatal("operation lock not respected")
	}
	if err := m.ControlDomain(DomainAction{Host: "www.youtube.com", Action: "pause"}); err != nil {
		t.Fatal(err)
	}
	if m.Config().excludedBy("child.www.youtube.com") == "" {
		t.Fatal("domain pause did not cover subdomains")
	}
	if err := m.ControlDomain(DomainAction{Host: "www.youtube.com", Action: "resume"}); err != nil {
		t.Fatal(err)
	}
	saved, err := LoadConfig(path)
	if err != nil || len(saved.DomainPins) != 1 || len(saved.PausedDomains) != 0 {
		t.Fatalf("manual controls not persisted: %v", err)
	}
}

type startFaultBackend struct {
	testBackend
	fail bool
}

func (b *startFaultBackend) Start(c Config) error {
	if b.fail {
		b.fail = false
		return errors.New("injected start failure")
	}
	return b.testBackend.Start(c)
}

func TestDomainResetKeepsOtherHostsAndRollsBackOnRestartFailure(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "rollback"}[fail], func(t *testing.T) {
			c := adaptiveFixture(t)
			c.Enabled = true
			c.DomainPins = []DomainPin{{Host: "a.example.com", Strategy: "fake-md5", TLS: "tls12", Family: "ipv4", Port: 443}}
			c.PausedDomains = []string{"a.example.com"}
			if err := PrepareAdaptive(c); err != nil {
				t.Fatal(err)
			}
			rows := []AdaptiveHost{}
			for _, host := range []string{"a.example.com", "b.example.com"} {
				rows = append(rows, AdaptiveHost{Group: "discovered_p443_tls12_ipv4", Host: host, Strategy: "fake-md5", Status: "connected", Updated: 1000, Symptom: "rst", Network: "ppp0", Port: 443, Origin: "pool", LastSwitch: 900, WindowStart: 800, Switches: 2, HoldUntil: 1200, HoldReason: "limit"})
			}
			data := adaptiveFrame(7, rows, nil)
			for _, name := range []string{"state0.tsv", "state1.tsv"} {
				if err := os.WriteFile(filepath.Join(c.RuntimeDir, name), data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			os.WriteFile(filepath.Join(c.RuntimeDir, "autohosts.txt"), []byte("a.example.com\nb.example.com\n"), 0600)
			b := &startFaultBackend{testBackend: testBackend{running: true}, fail: fail}
			path := filepath.Join(t.TempDir(), "config.json")
			WriteJSON(path, c)
			m := NewManager(c, path, b, successfulProber{}, &LogBuffer{})
			err := m.ControlDomain(DomainAction{Host: "a.example.com", Action: "reset"})
			if (err != nil) != fail {
				t.Fatalf("unexpected reset result: %v", err)
			}
			if !b.Running() || len(m.Config().DomainPins) != 1 || len(m.Config().PausedDomains) != 1 {
				t.Fatal("reset changed manual controls or left engine stopped")
			}
			got := ReadAdaptive(c)
			if fail {
				restored, _ := os.ReadFile(filepath.Join(c.RuntimeDir, "state0.tsv"))
				if !bytes.Equal(restored, data) || len(got.Hosts) != 2 {
					t.Fatal("failed reset lost previous state")
				}
			} else {
				if len(got.Hosts) != 1 || got.Hosts[0].Host != "b.example.com" || got.Hosts[0].Switches != 2 || got.Hosts[0].HoldUntil != 1200 || len(got.Discovered) != 1 || got.Discovered[0] != "b.example.com" {
					t.Fatalf("reset damaged another host: %+v", got)
				}
				if _, _, _, err := readAdaptiveSlot(filepath.Join(c.RuntimeDir, "state1.tsv")); err != nil {
					t.Fatal("new frame not readable", err)
				}
			}
		})
	}
}

func TestLuaPinsAndPauseRespectTLSContextWithoutAutomaticRotation(t *testing.T) {
	c := adaptiveFixture(t)
	if err := PrepareAdaptive(c); err != nil {
		t.Fatal(err)
	}
	runAdaptiveLua(t, c, luaPacketHarness+`
pilot_adaptive=false
pilot_pins={{host='tv.example.com',id='hostfake-md5',tls='tls12',family='ipv4',port=443}}
local base={first='none',third=true}
for i=1,8 do assert(packet('tv.example.com',true,false,hello(false),base)=='hostfake-md5','pin was automatically changed') end
assert(packet('tv.example.com',false,true,hello(false),base)=='hostfake-md5')
assert(packet('tv.example.com',false,false,hello(true),base)=='none','TLS 1.3 inherited webOS pin')
assert(packet('tv.example.com',false,false,hello(false),{first='none',third=true,ipv6=true})=='none','IPv6 inherited IPv4 pin')
assert(packet('tv.example.com',false,false,hello(false),{first='none',third=true,port=8443})=='none','HTTPS ports mixed')
pilot_exclusions={'tv.example.com'}
packet('tv.example.com',false,false,hello(false),{noaction=true})
packet('child.tv.example.com',false,false,hello(false),{noaction=true})
`)
	got := ReadAdaptive(c)
	if len(got.Hosts) != 1 || got.Hosts[0].Origin != "manual" || got.Hosts[0].Status != "connected" {
		t.Fatalf("pinned response attributed incorrectly: %+v", got)
	}
}

func TestLuaSwitchBudgetsSurviveRestartAndRemainPerHost(t *testing.T) {
	c := adaptiveFixture(t)
	if err := PrepareAdaptive(c); err != nil {
		t.Fatal(err)
	}
	runAdaptiveLua(t, c, luaPacketHarness+`
pilot_cooldown=30;pilot_max_switches=2
local base={first='none',third=true};local h12=hello(false)
for i=1,2 do packet('unstable.example.com',true,false,h12,base) end
assert(packet('unstable.example.com',false,false,h12,base)=='fake-md5')
for i=1,2 do packet('unstable.example.com',true,false,h12,base) end
assert(packet('unstable.example.com',false,false,h12,base)=='fake-md5','cooldown ignored')
autostate=nil;dofile(PILOT_SCRIPT)
for i=1,2 do packet('unstable.example.com',true,false,h12,base) end
assert(packet('unstable.example.com',false,false,h12,base)=='fake-md5','restart bypassed cooldown')
now=now+40
for i=1,2 do packet('unstable.example.com',true,false,h12,base) end
assert(packet('unstable.example.com',false,false,h12,base)=='hostfake-md5','cooldown never expired')
now=now+40
for i=1,2 do packet('unstable.example.com',true,false,h12,base) end
assert(packet('unstable.example.com',false,false,h12,base)=='hostfake-md5','ten-minute limit ignored')
for i=1,2 do packet('other.example.com',true,false,h12,base) end
assert(packet('other.example.com',false,false,h12,base)=='fake-md5','one host blocked another host rotation')
`)
	got := ReadAdaptive(c)
	held := map[string]bool{}
	for _, r := range got.History {
		if r.Status == "held" {
			held[r.HoldReason] = true
		}
	}
	if !held["cooldown"] || !held["limit"] {
		t.Fatalf("missing switch explanations: %+v", got.History)
	}
	for _, r := range got.Hosts {
		if r.Host == "unstable.example.com" && (r.Switches != 2 || r.HoldReason != "limit" || r.HoldUntil == 0) {
			t.Fatal("budget was not persisted")
		}
	}
}
