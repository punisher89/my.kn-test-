package pilot

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpgradeKeepsWorkingStrategiesAndAdaptiveOptOut(t *testing.T) {
	c := DefaultConfig()
	c.Profiles[0].Strategy = "fake-md5"
	c.Profiles[1].Strategy = "disorder"
	legacy := map[string]any{}
	data, _ := json.Marshal(c)
	json.Unmarshal(data, &legacy)
	delete(legacy, "adaptive")
	delete(legacy, "runtime_dir")
	delete(legacy, "probe_load_guard")
	delete(legacy, "adaptive_global_max_switches")
	path := filepath.Join(t.TempDir(), "config.json")
	if err := WriteJSON(path, legacy); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.ProbeLoadGuard || loaded.AdaptiveGlobalMaxSwitches != 30 || loaded.Adaptive || loaded.RuntimeDir == "" || loaded.Profiles[0].Strategy != "fake-md5" || loaded.Profiles[1].Strategy != "disorder" {
		t.Fatalf("working installation changed: %+v", loaded)
	}
}

func TestActiveTuningSuspendsPassiveRotationAndRestoresIt(t *testing.T) {
	c := DefaultConfig()
	c.Enabled, c.Adaptive = true, true
	c.Profiles[1].Strategy = "fake-md5"
	b := &testBackend{running: true}
	path := filepath.Join(t.TempDir(), "config.json")
	m := NewManager(c, path, b, successfulProber{}, &LogBuffer{})
	m.job = &Job{}
	m.tune(context.Background(), c, 0)
	if m.job.Stage != "done" || len(b.configs) != 2 || !b.configs[0].tuning || b.configs[1].tuning || !b.configs[1].Adaptive {
		t.Fatalf("passive rotation interfered with confirmation or failed to resume: %+v", m.job)
	}
	saved, err := LoadConfig(path)
	if err != nil || !saved.Adaptive || saved.Profiles[1].Strategy != "fake-md5" || m.Config().tuning {
		t.Fatalf("saved wrong state: %+v, %v", saved, err)
	}
}

func TestFailedActiveCandidateStopsEarlyWithoutIncompleteSuccess(t *testing.T) {
	c := DefaultConfig()
	p := Profile{Targets: []Target{{URL: "https://127.0.0.1/", Status: 200}}}
	full := HTTPProber{}.Check(context.Background(), p, c, false)
	c.probeFailFast = true
	fast := HTTPProber{}.Check(context.Background(), p, c, false)
	if len(full) != 4 || len(fast) != 1 || allPassed(fast, p, c) {
		t.Fatalf("failed candidate handling: full=%d fast=%+v", len(full), fast)
	}
}

func adaptiveFixture(t *testing.T) Config {
	t.Helper()
	source := os.Getenv("PILOT_TEST_ENGINE")
	if source == "" {
		t.Skip("native nfqws2 required")
	}
	base := t.TempDir()
	engine := filepath.Join(base, "engine")
	if err := os.MkdirAll(filepath.Join(base, "lua"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("cp", "-R", source, engine).Run(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile("../../lua/pilot-auto.lua")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(base, "lua/pilot-auto.lua"), data, 0644); err != nil {
		t.Fatal(err)
	}
	c := DefaultConfig()
	c.Adaptive = true
	c.EngineDir = engine
	c.RuntimeDir = filepath.Join(base, "state")
	return c
}

func TestAdaptiveNativeArgumentsAndLuaPaths(t *testing.T) {
	c := adaptiveFixture(t)
	if err := CheckEngine(c); err != nil {
		t.Fatal(err)
	}
	args := strings.Join(EngineArgs(c), " ")
	for _, part := range []string{"--lua-desync=pilot_auto", "zapret-auto.lua.gz", "pilot-auto.lua", "key=youtube_split-sni", "key=discovered", "pilot_id=none"} {
		if !strings.Contains(args, part) {
			t.Fatalf("missing adaptive argument: %s", part)
		}
	}
	normal := c
	normal.Adaptive = false
	if strings.Contains(strings.Join(EngineArgs(normal), " "), "pilot_auto:") {
		t.Fatal("discovery enabled despite opt-out")
	}
	c.tuning = true
	if strings.Contains(strings.Join(EngineArgs(c), " "), "pilot_auto:") {
		t.Fatal("passive rotation would invalidate active strategy tests")
	}
}

func TestAdaptiveStateRejectsIncompleteAndKeepsPreviousSlot(t *testing.T) {
	c := adaptiveFixture(t)
	if err := PrepareAdaptive(c); err != nil {
		t.Fatal(err)
	}
	previous := "PILOT1\t1\ndiscovered\texample.com\tsplit-sni\tconnected\t123\nEND\t1\n"
	if err := os.WriteFile(filepath.Join(c.RuntimeDir, "state1.tsv"), []byte(previous), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(c.RuntimeDir, "state0.tsv"), []byte("PILOT1\t2\ndiscovered\tinterrupted"), 0600); err != nil {
		t.Fatal(err)
	}
	got := ReadAdaptive(c)
	if got.Generation != 1 || len(got.Hosts) != 1 || got.Hosts[0].Host != "example.com" {
		t.Fatalf("partial write lost previous state: %+v", got)
	}
	path := filepath.Join(c.RuntimeDir, "autohosts.txt")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(filepath.Dir(c.RuntimeDir), "private")
	os.WriteFile(victim, []byte("preserve"), 0600)
	if err := os.Symlink(victim, path); err != nil {
		t.Fatal(err)
	}
	if err := PrepareAdaptive(c); err == nil {
		t.Fatal("symlink followed by privileged runtime preparation")
	}
	data, _ := os.ReadFile(victim)
	if string(data) != "preserve" {
		t.Fatal("foreign data overwritten")
	}
}

func TestLuaRotationIsolationPersistenceAndTLS12Alerts(t *testing.T) {
	c := adaptiveFixture(t)
	if err := PrepareAdaptive(c); err != nil {
		t.Fatal(err)
	}
	script := luaPacketHarness + `
assert(packet('a.example.com',false,false)=='split-sni')
assert(packet('a.example.com',true,false)=='split-sni')
assert(packet('a.example.com',true,false)=='split-sni','existing flow changed strategy')
assert(packet('a.example.com',false,false)=='fake-md5','new flow did not get next strategy')
assert(packet('b.example.com',false,true)=='split-sni')
assert(packet('a.example.com',false,true)=='fake-md5')
autostate=nil
dofile(PILOT_SCRIPT)
assert(packet('a.example.com',false,false)=='fake-md5','chosen strategy did not survive reload')
local alert=string.char(21,3,3,0,2,2,40)
assert(packet('c.example.com',false,false,alert)=='split-sni')
assert(packet('c.example.com',false,false,alert)=='split-sni','in-flight TLS alert changed method')
assert(packet('c.example.com',false,false)=='fake-md5','early TLS 1.2 alert not detected')
assert(packet('a.example.com',false,true)=='fake-md5','other domain rotated')
local h12,h13=hello(false),hello(true)
assert(packet('tv.example.com',true,false,h12)=='split-sni')
assert(packet('tv.example.com',true,false,h12)=='split-sni')
assert(packet('tv.example.com',false,false,h12)=='fake-md5')
assert(packet('tv.example.com',false,true,h13)=='split-sni','TLS 1.3 inherited webOS failures')
assert(packet('tv.example.com',false,true,h12,{ipv6=true})=='split-sni','IPv6 inherited IPv4 failures')
autostate=nil;dofile(PILOT_SCRIPT)
assert(packet('tv.example.com',false,false,h12)=='fake-md5','TLS choice not restored')
assert(packet('tv.example.com',false,false,h13)=='split-sni','TLS 1.3 choice overwritten')
packet(nil,false,false)
-- Healthy unknown sites do not get persisted or added to discovery.
assert(packet('healthy.example.com',false,true,h13,{first='none'})=='none')
-- Bound native transient storage even for large numbers of healthy sites.
for i=1,1100 do packet('healthy'..i..'.example.com',false,true,h13,{first='none'}) end
local n=0;for _ in pairs(autostate.discovered_p443_tls13_ipv4) do n=n+1 end
assert(n<=1024,'unbounded transient host records: '..n)
-- Exercise the real upstream retransmission detector (kernel counters mocked).
local opts={outgoing=true,retrans=true}
assert(packet('retry.example.com',false,false,h12,opts)=='split-sni')
assert(packet('retry.example.com',false,false,h12,opts)=='split-sni')
assert(packet('retry.example.com',false,false,h12)=='fake-md5')

`

	runAdaptiveLua(t, c, script)
	got := ReadAdaptive(c)
	for _, host := range got.Discovered {
		if strings.HasPrefix(host, "healthy") {
			t.Fatal("healthy site persisted as discovered")
		}
	}
	if len(got.Hosts) < 2 {
		t.Fatalf("observations not persisted: %+v", got)
	}
}

func runAdaptiveLua(t *testing.T, c Config, script string) {
	t.Helper()

	archive, err := os.Open(filepath.Join(c.EngineDir, "lua/zapret-auto.lua.gz"))
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	reader, err := gzip.NewReader(archive)
	if err != nil {
		t.Fatal(err)
	}
	auto, err := io.ReadAll(reader)
	reader.Close()
	if err != nil {
		t.Fatal(err)
	}
	prelude := `VERDICT_PASS=0
DLOG=function() end
DLOG_ERR=function() end
orchestrate=function(ctx,d) assert(d.plan,'missing test execution plan') end
plan_instance_pop=function(d) return table.remove(d.plan,1) end
apply_execution_plan=function(d,i) d.arg=i.arg end
`
	program := prelude + "\n" + string(auto) + "\npilot_runtime=" + luaString(c.RuntimeDir) + "\nPILOT_SCRIPT=" + luaString(filepath.Join(filepath.Dir(c.EngineDir), "lua/pilot-auto.lua")) + "\npilot_network=\"ppp0\"\npilot_reuse=true\npilot_cooldown=0\npilot_max_switches=1000\npilot_global_max_switches=10000\ndofile(PILOT_SCRIPT)\n" + script
	path := filepath.Join(filepath.Dir(c.RuntimeDir), "controller-test.lua")
	if err = os.WriteFile(path, []byte(program), 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("python3", "../../scripts/run-lua-test.py", path, c.RuntimeDir).CombinedOutput(); err != nil {
		t.Fatalf("Lua controller test failed: %v\n%s", err, out)
	}
}

const luaPacketHarness = `
local now=1000
os.time=function() return now end
local sent={}
TH_RST,TH_FIN=4,1
bitand=function(a,b)
 local result,p=0,1
 while a>0 and b>0 do
  if a%2==1 and b%2==1 then result=result+p end
  a=math.floor(a/2);b=math.floor(b/2);p=p*2
 end
 return result
end
pos_get=function(d) return d.success and 5000 or 1 end
is_retransmission=function(d) return d.retrans or false end
plan_instance_execute=function(d,v,i) apply_execution_plan(d,i);sent[#sent+1]=i.arg.pilot_id;return VERDICT_PASS end
local function packet(host,fail,success,payload,opts)
 opts=opts or {}
 now=now+3;sent={}
 pilot_network=opts.network or 'ppp0';pilot_reuse=opts.reuse~=false
 local d={track=opts.track or {hostname=host,lua_state={}},arg={key=opts.key or 'discovered',reqhost='1',fails='2',time='60',retrans='1',failure_detector='pilot_failure',success_detector='pilot_success'},func_instance='pilot_auto_1',outgoing=false,dis={tcp={th_flags=fail and TH_RST or 0,th_sport=opts.port or 443,th_dport=opts.port or 443},ip=not opts.ipv6 and {} or nil,ip6=opts.ipv6 and {} or nil,payload=payload or (success and 'reply' or '')},fail=fail,success=success,plan={
 {arg={strategy='1',pilot_id=opts.first or 'split-sni'},func='pilot_pass'},
 {arg={strategy='2',pilot_id='fake-md5'},func='pilot_pass'}}}
 if opts.third then d.plan[#d.plan+1]={arg={strategy='3',pilot_id='hostfake-md5'},func='pilot_pass'} end
 if opts.outgoing then d.outgoing=true end
 if opts.retrans then d.retrans=true end
 pilot_auto(nil,d);if not host or opts.noaction then assert(#sent==0,'actions ran without SNI');return end
 assert(#sent==1,host..": unexpected action count "..#sent);return sent[1]
end
-- A framing-complete ClientHello with/without supported_versions=TLS 1.3.
local function hello(tls13)
 local function u16(n) return string.char(math.floor(n/256),n%256) end
 local ext=tls13 and u16(43)..u16(3)..string.char(2,3,4) or ''
 local body=string.char(3,3)..string.rep('r',32)..string.char(0)..u16(2)..string.char(0,47,1,0)..u16(#ext)..ext
 local handshake=string.char(1,0,0,#body)..body
 return string.char(22,3,1)..u16(#handshake)..handshake
end
`

func TestLearnedCandidatesRequireCompatibleEvidenceAndOwnSuccess(t *testing.T) {
	c := adaptiveFixture(t)
	if err := PrepareAdaptive(c); err != nil {
		t.Fatal(err)
	}
	runAdaptiveLua(t, c, luaPacketHarness+`
local h12,h13=hello(false),hello(true)
local base={third=true,first='none'}
-- Discover a working third method through the real upstream circular API.
for i=1,2 do assert(packet('donor.example.com',true,false,h12,base)=='none') end
assert(packet('donor.example.com',false,false,h12,base)=='fake-md5')
for i=1,2 do assert(packet('donor.example.com',true,false,h12,base)=='fake-md5') end
assert(packet('donor.example.com',false,true,h12,base)=='hostfake-md5')
assert(packet('donor.example.com',false,false,h12,{third=true,first='none',port=8443})=='none','same host mixed HTTPS ports')
assert(packet('donor.example.com',false,false,h12,base)=='hostfake-md5')
-- New destinations always start with their own normal connection.
assert(packet('new.example.com',false,false,h12,base)=='none')
assert(packet('new.example.com',true,false,h12,base)=='none')
local oldflow={hostname='new.example.com',lua_state={}}
local inflight={third=true,first='none',track=oldflow}
assert(packet('new.example.com',true,false,h12,inflight)=='none')
assert(packet('new.example.com',false,false,h12,inflight)=='none','in-flight strategy changed')
assert(packet('new.example.com',false,false,h12,base)=='hostfake-md5','learned candidate not preferred')
-- A donor response is not a response from the new site. The new site must
-- produce its own success before it becomes another donor.
assert(autostate.discovered_p443_tls12_ipv4['new.example.com'].pilot_origin=='learned')
for i=1,2 do assert(packet('new.example.com',true,false,h12,base)=='hostfake-md5') end
assert(packet('new.example.com',false,false,h12,base)=='fake-md5','fallback repeated the learned candidate')
for i=1,2 do assert(packet('new.example.com',true,false,h12,base)=='fake-md5') end
assert(packet('new.example.com',false,false,h12,base)=='none','cycle never returned to normal connection')
-- Different TLS, IP, port, WAN, failure symptom and opt-out cannot inherit.
local cases={
 {'missing.example.com',h12,{first='none'}},
 {'unknown.example.com','',{third=true,first='none'}},
 {'tls.example.com',h13,{third=true,first='none'}},
 {'ipv6.example.com',h12,{third=true,first='none',ipv6=true}},
 {'port.example.com',h12,{third=true,first='none',port=8443}},
 {'wan.example.com',h12,{third=true,first='none',network='ppp1'}},
 {'off.example.com',h12,{third=true,first='none',reuse=false}}
}
for _,c in ipairs(cases) do
 for i=1,2 do assert(packet(c[1],true,false,c[2],c[3])=='none') end
 assert(packet(c[1],false,false,c[2],c[3])=='fake-md5','incompatible observation reused: '..c[1])
end
local retry={third=true,first='none',outgoing=true,retrans=true}
for i=1,2 do assert(packet('symptom.example.com',false,false,h12,retry)=='none') end
assert(packet('symptom.example.com',false,false,h12,base)=='fake-md5','RST experience reused for retransmission')
-- Choices and their attribution survive a controller reload.
autostate=nil;dofile(PILOT_SCRIPT)
assert(packet('donor.example.com',false,false,h12,base)=='hostfake-md5')
for i=1,2 do packet('restored.example.com',true,false,h12,base) end
assert(packet('restored.example.com',false,true,h12,base)=='hostfake-md5','learned evidence lost after restart')
-- Older observations expire. Fresh failures still use the full fallback pool.
now=now+8*24*60*60
for i=1,2 do packet('expired.example.com',true,false,h12,base) end
assert(packet('expired.example.com',false,false,h12,base)=='fake-md5','expired experience reused')
assert(packet('donor.example.com',false,true,h12,base)=='hostfake-md5')
for i=1,2 do packet('fresh.example.com',true,false,h12,base) end
assert(packet('fresh.example.com',false,false,h12,base)=='hostfake-md5','fresh response did not renew evidence')
`)
	got := ReadAdaptive(c)
	if len(got.Boxes) < 5 || len(got.History) == 0 {
		t.Fatalf("missing groups/history: %+v", got)
	}
	var learned bool
	for _, r := range got.Hosts {
		if r.Host == "restored.example.com" {
			learned = r.Origin == "learned" && r.Status == "connected" && r.Symptom == "rst"
		}
	}
	if !learned {
		t.Fatal("candidate attribution or own success was not persisted")
	}
}
