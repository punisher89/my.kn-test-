package pilot

import (
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type activeRouterBackend struct{ *testBackend }

func (*activeRouterBackend) Kind() string { return "router" }
func activeFixture(t *testing.T) (*Manager, Config, *testBackend, DomainScope) {
	t.Helper()
	c := DefaultConfig()
	c.Adaptive = true
	c.Enabled = true
	c.WAN = "ppp0"
	c.RuntimeDir = filepath.Join(t.TempDir(), "runtime")
	c.ProbeLoadGuard = false
	c.Attempts = 3
	if err := PrepareAdaptive(c); err != nil {
		t.Fatal(err)
	}
	backend := &testBackend{running: true}
	m := NewManager(c, filepath.Join(t.TempDir(), "config.json"), &activeRouterBackend{backend}, successfulProber{}, &LogBuffer{})
	scope := DomainScope{"www.youtube.com", "tls13", "ipv4", 443}
	m.domainJob = &DomainSearch{Scope: scope, Stage: "direct", Started: time.Now(), Trials: []Trial{}}
	return m, c, backend, scope
}
func evidence(ok, executed bool) activeEvidence {
	r := ProbeResult{OK: ok, URL: "https://www.youtube.com/", TLS: "1.3", Family: "tcp4"}
	if !ok {
		r.Error = "connection reset by peer"
	}
	return activeEvidence{Result: r, Executed: executed}
}

func TestActiveSearchMeasuresAllCandidatesWithoutRestartingLiveEngine(t *testing.T) {
	m, c, b, s := activeFixture(t)
	calls := []string{}
	m.activeProbeHook = func(_ context.Context, _ Config, _ DomainScope, _ Target, id string) activeEvidence {
		calls = append(calls, id)
		return evidence(id == "fake-md5", true)
	}
	stage, _ := m.searchDomain(context.Background(), c, s)
	if stage != "applied" {
		t.Fatalf("search did not apply: %+v", m.domainJob)
	}
	if len(b.configs) != 0 || !b.Running() {
		t.Fatal("candidate search restarted live engine")
	}
	if len(calls) != 9 || calls[0] != "direct" || calls[1] != "direct" || calls[len(calls)-1] != "fake-md5" {
		t.Fatalf("missing screening/full confirmation: %+v", calls)
	}
	plan, ok := m.planFor(c, s)
	if !ok || plan.Strategy != "fake-md5" {
		t.Fatal("verified plan missing")
	}
	text, err := os.ReadFile(filepath.Join(c.RuntimeDir, "verified-domains.tsv"))
	if err != nil || !strings.Contains(string(text), "www.youtube.com\ttls13\tipv4\t443\tfake-md5") {
		t.Fatal("runtime plan not published", err)
	}
	restored := NewManager(c, m.path, &activeRouterBackend{b}, successfulProber{}, &LogBuffer{})
	if p, ok := restored.planFor(c, s); !ok || p.Strategy != "fake-md5" {
		t.Fatal("verified method not durable")
	}
	changed := cloneConfig(c)
	changed.WAN = "ppp1"
	if _, ok := restored.planFor(changed, s); ok {
		t.Fatal("plan crossed WAN context")
	}
	if _, ok := restored.planFor(c, DomainScope{s.Host, "tls12", s.Family, s.Port}); ok {
		t.Fatal("plan crossed TLS version")
	}
	if restored.Config().Profiles[0].Strategy != c.Profiles[0].Strategy {
		t.Fatal("domain search overwrote whole service")
	}
}

func TestActiveSearchNeverAcceptsMissingProofOrHTTPRejection(t *testing.T) {
	for _, mode := range []string{"missing-proof-success", "missing-proof-failure", "403", "451", "dns", "mixed-direct"} {
		t.Run(mode, func(t *testing.T) {
			m, c, _, s := activeFixture(t)
			calls := 0
			m.activeProbeHook = func(_ context.Context, _ Config, _ DomainScope, _ Target, id string) activeEvidence {
				calls++
				if mode == "mixed-direct" {
					return evidence(calls == 1, true)
				}
				if mode == "403" || mode == "451" {
					e := evidence(false, true)
					if mode == "403" {
						e.Result.Status = 403
					} else {
						e.Result.Status = 451
					}
					e.Result.Error = "HTTP rejected"
					e.Local = true
					return e
				}
				if mode == "dns" {
					e := evidence(false, true)
					e.Local = true
					e.Result.Error = "DNS: unavailable"
					return e
				}
				if id == "direct" {
					return evidence(false, true)
				}
				return evidence(mode == "missing-proof-success", false)
			}
			stage, _ := m.searchDomain(context.Background(), c, s)
			if stage != "unmeasured" || len(m.domainPlans.Plans) != 0 {
				t.Fatalf("accepted unmeasured experience: %+v", m.domainJob)
			}
			if (mode == "403" || mode == "451" || mode == "dns") && calls != 1 {
				t.Fatal("server/auth/DNS error started candidate scan")
			}
		})
	}
}

func TestActiveSearchRetriesUnconfirmedCandidateWithBoundedBackoff(t *testing.T) {
	m, c, _, s := activeFixture(t)
	task := &domainTask{Scope: s, Signal: time.Now().Unix()}
	m.domainTasks = map[DomainScope]*domainTask{s: task}
	firstCandidate := ""
	m.activeProbeHook = func(_ context.Context, _ Config, _ DomainScope, _ Target, id string) activeEvidence {
		if id == "direct" {
			return evidence(false, true)
		}
		if firstCandidate == "" {
			firstCandidate = id
		}
		if id != firstCandidate {
			t.Fatal("unmeasured candidate was skipped", id)
		}
		return evidence(false, false)
	}
	for _, delay := range []time.Duration{30, 60, 120, 240, 300, 300} {
		m.domainJob = &DomainSearch{Scope: s, Trials: []Trial{}}
		start := time.Now()
		stage, next := m.searchDomain(context.Background(), c, s)
		if stage != "unmeasured" || m.domainJob.Reason != "execution_unconfirmed" || len(m.domainPlans.Plans) != 0 {
			t.Fatalf("unconfirmed method accepted or reason lost: %+v", m.domainJob)
		}
		if got := next.Sub(start); got < delay*time.Second || got > delay*time.Second+time.Second {
			t.Fatalf("retry delay %v, expected %v seconds", got, delay)
		}
	}
	m.activeProbeHook = func(_ context.Context, _ Config, _ DomainScope, _ Target, id string) activeEvidence {
		return evidence(id == firstCandidate, true)
	}
	m.domainJob = &DomainSearch{Scope: s, Trials: []Trial{}}
	if stage, _ := m.searchDomain(context.Background(), c, s); stage != "applied" {
		t.Fatal("search did not recover after proof returned", stage)
	}
}

func TestActiveQueueGivesUntriedDomainTurnBeforeReadyRetry(t *testing.T) {
	m, c, _, s := activeFixture(t)
	if err := os.WriteFile(filepath.Join(c.RuntimeDir, "state1.tsv"), adaptiveFrame(3, nil, nil), 0600); err != nil {
		t.Fatal(err)
	}
	other := DomainScope{"i.ytimg.com", s.TLS, s.Family, s.Port}
	now := time.Now()
	m.domainTasks = map[DomainScope]*domainTask{
		s:     {Scope: s, Signal: now.Unix() - 20, Attempted: now.Add(-time.Minute)},
		other: {Scope: other, Signal: now.Unix() - 10},
	}
	seen := []DomainScope{}
	m.activeProbeHook = func(_ context.Context, _ Config, scope DomainScope, _ Target, _ string) activeEvidence {
		seen = append(seen, scope)
		return activeEvidence{Local: true, Result: ProbeResult{Status: 403, Error: "HTTP rejected"}}
	}
	m.activeTick(context.Background())
	if len(seen) != 1 || seen[0] != other {
		t.Fatal("old failure starved untried domain", seen)
	}
	if m.domainJob.Reason != "http_response" {
		t.Fatal("HTTP failure not explained", m.domainJob)
	}
	m.domainTasks[other].Next = time.Time{}
	m.activeTick(context.Background())
	if len(seen) != 2 || seen[1] != s {
		t.Fatal("ready tasks did not alternate", seen)
	}
}

func TestConfiguredServiceEntersFullQueueAndGetsPriority(t *testing.T) {
	m, c, _, s := activeFixture(t)
	// Stress-test legacy opt-in broad discovery, while alpha.23 defaults to only services.
	c.BackgroundOtherDomains=true
	m.c.BackgroundOtherDomains=true
	now := time.Now().Unix()
	m.domainTasks = map[DomainScope]*domainTask{}
	for i := 0; i < 64; i++ {
		unknown := DomainScope{fmt.Sprintf("host%d.example.net", i), s.TLS, s.Family, s.Port}
		m.domainTasks[unknown] = &domainTask{Scope: unknown, Signal: now - 10}
	}
	event := AdaptiveHost{EventCount: 1, FirstUpdated: now - 1, Symptom: "retransmission", Group: "youtube_split-sni_p443_tls13_ipv4", Host: s.Host, Strategy: "split-sni", Status: "failed", Updated: now - 1, Network: c.WAN, Port: s.Port, Origin: "initial"}
	if err := os.WriteFile(filepath.Join(c.RuntimeDir, "state1.tsv"), adaptiveFrame(3, nil, []AdaptiveHost{event}), 0600); err != nil {
		t.Fatal(err)
	}
	m.activeProbeHook = func(_ context.Context, _ Config, scope DomainScope, _ Target, _ string) activeEvidence {
		if scope != s {
			t.Fatal("unrelated site ran ahead of configured service", scope)
		}
		return evidence(true, true)
	}
	m.activeTick(context.Background())
	if len(m.domainTasks) != 64 || m.domainTasks[s] == nil || m.domainJob.Scope != s || m.domainJob.Stage != "applied" {
		t.Fatal("configured service excluded from full queue", m.domainJob)
	}
}

func TestActiveMissingClientHelloExplainsWaitingWithoutProbes(t *testing.T) {
	m, c, _, s := activeFixture(t)
	stage, next := m.searchDomain(context.Background(), c, s)
	if stage != "unmeasured" || m.domainJob.Reason != "client_hello_missing" || next.IsZero() || len(m.domainJob.Trials) != 0 {
		t.Fatal("missing client sample did not explain waiting", m.domainJob)
	}
	if code := activeReasonCode("private-url-secret"); code != "" {
		t.Fatal("unknown diagnostic reason allowed", code)
	}
	m.domainJob.Reason = "private-url-secret"
	if m.Diagnostics().ActiveSearch.Reason != "" {
		t.Fatal("free text leaked into diagnostic reason")
	}
	m.domainJob.Reason = "client_hello_missing"
	if m.Diagnostics().ActiveSearch.Reason != "client_hello_missing" {
		t.Fatal("safe diagnostic reason missing")
	}
}
func TestActiveBudgetAppliesToInstallationNotCandidateExperiments(t *testing.T) {
	m, c, _, s := activeFixture(t)
	c.AdaptiveMaxSwitches = 1
	c.AdaptiveGlobalMaxSwitches = 1
	m.activeProbeHook = func(_ context.Context, _ Config, _ DomainScope, _ Target, id string) activeEvidence {
		return evidence(id == "fake-md5", true)
	}
	stage, _ := m.searchDomain(context.Background(), c, s)
	if stage != "applied" {
		t.Fatal("candidate count consumed installation budget")
	}
	if m.domainBudget(c, s, time.Now().Unix()) == 0 {
		t.Fatal("confirmed method change did not consume budget")
	}
	restored := NewManager(c, m.path, &activeRouterBackend{&testBackend{running: true}}, successfulProber{}, &LogBuffer{})
	if restored.domainBudget(c, s, time.Now().Unix()) == 0 {
		t.Fatal("restart reset live-change budget")
	}
}
func TestDomainPlanPublishFailureDoesNotPersistNewMethod(t *testing.T) {
	m, c, _, s := activeFixture(t)
	path := filepath.Join(c.RuntimeDir, "verified-domains.tsv")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := m.commitDomain(c, s, "fake-md5"); err == nil {
		t.Fatal("publish failure ignored")
	}
	if len(m.domainPlans.Plans) != 0 {
		t.Fatal("failed method entered memory store")
	}
	if _, err := os.Stat(m.planPath()); !os.IsNotExist(err) {
		t.Fatal("failed method entered durable store")
	}
}
func TestDomainQueueExclusionAndOwnedMarkRules(t *testing.T) {
	m, c, _, s := activeFixture(t)
	if err := m.QueueDomain(s); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.c.PausedDomains = []string{s.Host}
	m.mu.Unlock()
	if err := m.QueueDomain(s); err == nil {
		t.Fatal("paused domain queued")
	}
	for _, r := range FirewallRules(c) {
		if strings.Contains(strings.Join(r.Args, " "), "CONNMARK") {
			text := strings.Join(r.Args, " ")
			if !strings.Contains(text, "--comment keenetic-pilot") || !strings.Contains(text, "0x200000dc/0xffffffff") {
				t.Fatal("mark rule touches unrelated traffic", text)
			}
		}
	}
	args := strings.Join(EngineArgs(c), " ")
	if !strings.Contains(args, "--filter-mark=0x200000dc/0xffffffff") || !strings.Contains(args, "--lua-desync=pilot_trial:reqhost") || !strings.Contains(args, ":retrans=3:") {
		t.Fatal("missing isolated proof plane / retransmission threshold")
	}
}
func TestTrialProofRequiresExactNonceAndScope(t *testing.T) {
	_, c, _, s := activeFixture(t)
	nonce, err := trialCommand(c, s, "fake-md5")
	if err != nil {
		t.Fatal(err)
	}
	text := fmt.Sprintf("PILOT_PROOF1\t%s\t%s\tfake-md5\t%s\t%s\t%d\nEND\t%s\n", nonce, s.Host, s.TLS, s.Family, s.Port, nonce)
	if err := os.WriteFile(filepath.Join(c.RuntimeDir, "probe-proof.tsv"), []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	if !trialProof(c, s, "fake-md5", nonce) || trialProof(c, s, "split-sni", nonce) || trialProof(c, s, "fake-md5", strings.Repeat("b", 32)) {
		t.Fatal("proof accepted wrong method/nonce")
	}
}

func TestActiveSearchTriesEntireCatalogAndKeepsServingOnFailure(t *testing.T) {
	m, c, b, s := activeFixture(t)
	seen := map[string]bool{}
	m.activeProbeHook = func(_ context.Context, _ Config, _ DomainScope, _ Target, id string) activeEvidence {
		seen[id] = true
		return evidence(false, true)
	}
	stage, _ := m.searchDomain(context.Background(), c, s)
	if stage != "exhausted" || len(m.domainPlans.Plans) != 0 || len(b.configs) != 0 || !b.Running() {
		t.Fatal("failed search changed live service", stage)
	}
	for _, candidate := range StrategiesFor(c) {
		if candidate.ID != "none" && !seen[candidate.ID] {
			t.Fatal("catalog skipped candidate", candidate.ID)
		}
	}
	if activeContext(c) != activeContext(cloneConfig(c)) || domainFingerprint(c, s) != domainFingerprint(cloneConfig(c), s) {
		t.Fatal("nil/empty normalization changed execution context")
	}
}

func TestLuaActiveModeDefersUnconfirmedLiveChangesAndIsolatesMeasuredRoutes(t *testing.T) {
	c := adaptiveFixture(t)
	if err := PrepareAdaptive(c); err != nil {
		t.Fatal(err)
	}
	script := luaPacketHarness + `
local clean=assert(io.open(pilot_runtime..'/verified-domains.tsv','w'));clean:write('PILOT_VERIFIED1\tinitial-empty\n');clean:close()
local h12,h13=hello(false),hello(true);local opts={third=true}
for i=1,2 do packet('active.example.com',true,false,h13,opts) end
assert(packet('active.example.com',false,false,h13,opts)=='fake-md5')
-- The old probing choice remains in history, but is not a confirmed route.
pilot_active=true;autostate=nil;dofile(PILOT_SCRIPT)
dofile(PILOT_SCRIPT:gsub('pilot%-auto.lua','pilot-measure.lua'))
pilot_plan_context='fixture-context'
assert(packet('active.example.com',false,false,h13,opts)=='split-sni','unconfirmed legacy choice restored')
for i=1,2 do packet('active.example.com',true,false,h13,opts) end
assert(packet('active.example.com',false,false,h13,opts)=='split-sni','live traffic used as unverified trial')
local in_flight={hostname='active.example.com',lua_state={}}
packet('active.example.com',false,false,h13,{third=true,track=in_flight})
local f=assert(io.open(pilot_runtime..'/verified-domains.tsv','w'))
f:write('PILOT_VERIFIED1\tfixture-context\nactive.example.com\ttls13\tipv4\t443\thostfake-md5\t'..(now+600)..'\n');f:close()
assert(packet('active.example.com',false,false,h13,opts)=='hostfake-md5','verified method not installed')
assert(packet('active.example.com',false,false,h13,{third=true,track=in_flight})=='split-sni','in-flight connection switched')
assert(packet('active.example.com',false,false,h12,opts)=='split-sni','TLS scope leaked')
assert(packet('other.example.com',false,false,h13,opts)=='split-sni','domain scope leaked')
assert(packet('active.example.com',false,false,h13,{third=true,ipv6=true})=='split-sni','IP scope leaked')
pilot_plan_context='changed-settings'
assert(packet('active.example.com',false,false,h13,opts)=='split-sni','old configuration route applied')
`
	runAdaptiveLua(t, c, script)
}

func TestLuaTrialRequiresOwnedMarkScopeAndAcknowledgesActualExecution(t *testing.T) {
	c := adaptiveFixture(t)
	if err := PrepareAdaptive(c); err != nil {
		t.Fatal(err)
	}
	runAdaptiveLua(t, c, luaPacketHarness+`
dofile(PILOT_SCRIPT:gsub('pilot%-auto.lua','pilot-measure.lua'))
local nonce=string.rep('a',32);local payload=hello(true)
local function command(id,tls,expires)
 local f=assert(io.open(pilot_runtime..'/probe-command.tsv','w'))
 f:write('PILOT_TRIAL1\t'..nonce..'\ttrial.example.com\t'..id..'\t'..(tls or 'tls13')..'\tipv4\t443\t'..(expires or now+80)..'\n');f:close()
end
local function probe(mark,ifin,host,track)
 sent={};local d={track=track or {hostname=host or 'trial.example.com',lua_state={}},outgoing=true,fwmark=mark,ifin=ifin,arg={},func_instance='pilot_trial_1',dis={tcp={th_dport=443,th_sport=45000},ip={},payload=payload},plan={
 {arg={strategy='1',pilot_id='split-sni'},func='pilot_pass'},
 {arg={strategy='2',pilot_id='fake-md5'},func='pilot_pass'},
 {arg={strategy='3',pilot_id='hostfake-md5'},func='pilot_pass'}}}
 pilot_trial(nil,d);return sent
end
command('hostfake-md5')
assert(#probe(0,nil)==0,'unmarked user flow became a trial')
assert(#probe(0x200000dc,'br0')==0,'forwarded flow became a local probe')
assert(#probe(0x200000dc,nil,'different.example.com')==0,'wrong SNI got candidate')
command('hostfake-md5','tls12');assert(#probe(0x200000dc,nil)==0,'wrong TLS got candidate')
command('hostfake-md5','tls13',now-1);assert(#probe(0x200000dc,nil)==0,'expired trial applied')
command('hostfake-md5')
local track={hostname='trial.example.com',lua_state={}}
local sent=probe(0x200000dc,nil,nil,track);assert(#sent==1 and sent[1]=='hostfake-md5')
local f=assert(io.open(pilot_runtime..'/probe-proof.tsv','r'));local proof=f:read('*a');f:close()
assert(proof=='PILOT_PROOF1\t'..nonce..'\ttrial.example.com\thostfake-md5\ttls13\tipv4\t443\nEND\t'..nonce..'\n','execution not acknowledged exactly')
command('fake-md5')
assert(probe(0x200000dc,nil,nil,track)[1]=='hostfake-md5','in-flight probe changed candidate')
assert(probe(0x200000dc,nil)[1]=='fake-md5','new probe did not pick new candidate')
`)
}

func TestActiveSchedulerRunsFromFailureHistoryWithoutClientRetry(t *testing.T) {
	m, c, b, s := activeFixture(t)
	now := time.Now().Unix()
	event := AdaptiveHost{EventCount: 1, FirstUpdated: now - 1, Group: "youtube_split-sni_p443_tls13_ipv4", Host: s.Host, Strategy: "split-sni", Status: "failed", Updated: now - 1, Symptom: "retransmission", Network: "ppp0", Port: 443, Origin: "initial"}
	if err := os.WriteFile(filepath.Join(c.RuntimeDir, "state1.tsv"), adaptiveFrame(3, nil, []AdaptiveHost{event}), 0600); err != nil {
		t.Fatal(err)
	}
	probes := 0
	m.activeProbeHook = func(_ context.Context, _ Config, _ DomainScope, _ Target, id string) activeEvidence {
		probes++
		return evidence(id == "fake-md5", true)
	}
	m.activeTick(context.Background())
	if probes == 0 || m.Snapshot().DomainJob == nil || m.Snapshot().DomainJob.Stage != "applied" {
		t.Fatal("scheduler waited for another client request")
	}
	previous := probes
	m.activeTick(context.Background())
	if probes != previous {
		t.Fatal("same old evidence retriggered a completed search")
	}
	if len(b.configs) != 0 {
		t.Fatal("scheduler restarted router engine")
	}
}

func TestActiveSchedulerYieldsToRequestedUpdate(t *testing.T) {
	m, c, _, s := activeFixture(t)
	if err := os.WriteFile(filepath.Join(c.RuntimeDir, "state1.tsv"), adaptiveFrame(3, nil, nil), 0600); err != nil {
		t.Fatal(err)
	}
	m.domainTasks = map[DomainScope]*domainTask{s: {Scope: s, Signal: time.Now().Unix()}}
	probes := 0
	m.activeProbeHook = func(_ context.Context, _ Config, _ DomainScope, _ Target, _ string) activeEvidence {
		probes++
		return evidence(true, true)
	}
	m.updateRequested.Store(true)
	m.activeTick(context.Background())
	if probes != 0 {
		t.Fatal("background search started ahead of requested update")
	}
	m.updateRequested.Store(false)
	m.activeTick(context.Background())
	if probes == 0 {
		t.Fatal("background search did not resume after request cleared")
	}
}

func TestActiveDomainEndpointAuthAndDiagnosticAllowlist(t *testing.T) {
	m, _, _, s := activeFixture(t)
	token := strings.Repeat("a", 40)
	handler := Handler(m, token)
	body := fmt.Sprintf(`{"host":%q,"tls":%q,"family":%q,"port":%d}`, s.Host, s.TLS, s.Family, s.Port)
	for _, test := range []struct {
		auth, origin string
		code         int
	}{{"", "", 401}, {"Bearer " + token, "http://other.test", 403}, {"Bearer " + token, "", 202}} {
		req := httptest.NewRequest("POST", "http://router.test/api/domain-search", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", test.auth)
		req.Header.Set("Origin", test.origin)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		if response.Code != test.code {
			t.Fatalf("domain-search authorization: %d != %d", response.Code, test.code)
		}
	}
	m.domainJob = &DomainSearch{Scope: s, Stage: "search", Candidate: "fake-md5", Started: time.Now(), Error: "private-error-secret", Trials: []Trial{{Strategy: "fake-md5", Error: "private-trial-secret", Results: []ProbeResult{{URL: "https://private-query-secret.test/"}}}}}
	req := httptest.NewRequest("GET", "http://router.test/api/diagnostics", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	for _, secret := range []string{"private-error-secret", "private-trial-secret", "private-query-secret"} {
		if strings.Contains(response.Body.String(), secret) {
			t.Fatal("active diagnostic leaked private probe data", secret)
		}
	}
	if !strings.Contains(response.Body.String(), "active_search") || !strings.Contains(response.Body.String(), s.Host) {
		t.Fatal("active progress absent from diagnostic")
	}
}
