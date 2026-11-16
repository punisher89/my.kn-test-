package pilot

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestTuningOrderKeepsPoolAndUsesOnlyRelevantEvidence(t *testing.T) {
	c := DefaultConfig()
	c.WAN = "ppp0"
	p := c.Profiles[0]
	p.Strategy = "hostfake-md5"
	now := time.Now().Unix()
	box := AdaptiveBox{TLS: "tls13", Family: "ipv4", Network: "ppp0", Port: 443, Hosts: []AdaptiveHost{{Host: "www.youtube.com"}}, Methods: []AdaptiveMethod{{ID: "fake-md5", Reusable: true, Latest: now}}}
	a := AdaptiveSnapshot{Boxes: []AdaptiveBox{box}, History: []AdaptiveHost{{Host: "www.youtube.com", Network: "ppp0", Port: 443, Status: "failed", Strategy: "split-sni", Updated: now}}}
	order := tuningStrategies(c, p, a, now)
	if order[0].ID != p.Strategy || order[1].ID != "fake-md5" || order[len(order)-1].ID != "split-sni" {
		t.Fatalf("wrong priorities: %+v", order)
	}
	if len(order) != len(StrategiesFor(c)) {
		t.Fatal("history removed fallbacks")
	}
	for _, kind := range []string{"wan", "ipv6", "port", "stale", "disabled", "manual"} {
		altered := box
		altered.Hosts = append([]AdaptiveHost{}, box.Hosts...)
		altered.Methods = append([]AdaptiveMethod{}, box.Methods...)
		config := c
		switch kind {
		case "wan":
			altered.Network = "ppp1"
		case "ipv6":
			altered.Family = "ipv6"
		case "port":
			altered.Port = 8443
		case "stale":
			altered.Methods[0].Latest = now - 8*24*60*60
		case "disabled":
			config.AdaptiveReuse = false
		case "manual":
			altered.Hosts[0].Pinned = "fake-md5"
		}
		got := tuningStrategies(config, p, AdaptiveSnapshot{Boxes: []AdaptiveBox{altered}}, now)
		if got[0].ID != p.Strategy || got[1].ID != "none" {
			t.Fatalf("incompatible priority (%s): %+v", kind, got)
		}
	}
}

type screeningProber struct {
	attempts []int
	methods  []string
}

func (p *screeningProber) Check(_ context.Context, profile Profile, c Config, direct bool) []ProbeResult {
	id := "direct"
	if !direct {
		for _, v := range c.Profiles {
			if v.ID == profile.ID {
				id = v.Strategy
			}
		}
	}
	p.attempts = append(p.attempts, c.Attempts)
	p.methods = append(p.methods, id)
	// Saved candidate is fast but unstable; only split-sni survives full confirmation.
	if id == "split-sni" || (id == "hostfake-md5" && c.Attempts == 1) {
		return successfulResults(profile, c, direct)
	}
	return []ProbeResult{{URL: profile.Targets[0].URL, Error: "simulated failure"}}
}

func TestFastScreeningStillConfirmsFullyAndContinuesAfterFlakyCandidate(t *testing.T) {
	c := DefaultConfig()
	c.Enabled = true
	c.WAN = "ppp0"
	c.Attempts = 3
	c.IPv6 = true
	c.Profiles[0].Strategy = "hostfake-md5"
	path := filepath.Join(t.TempDir(), "config.json")
	if err := WriteJSON(path, c); err != nil {
		t.Fatal(err)
	}
	p := &screeningProber{}
	m := NewManager(c, path, &testBackend{running: true}, p, &LogBuffer{})
	m.job = &Job{}
	m.tune(context.Background(), c, 0)
	job := m.Snapshot().Job
	if job.Selected != "split-sni" || job.Stage != "done" {
		t.Fatalf("did not move past unstable method: %+v", job)
	}
	wantMethods := []string{"direct", "hostfake-md5", "hostfake-md5", "split-sni", "split-sni"}
	wantAttempts := []int{1, 1, 3, 1, 3}
	if len(p.methods) != len(wantMethods) {
		t.Fatalf("unexpected probes: %+v", p)
	}
	for i := range wantMethods {
		if p.methods[i] != wantMethods[i] || p.attempts[i] != wantAttempts[i] {
			t.Fatalf("screen or confirmation changed: %+v", p)
		}
	}
	saved, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Attempts != 3 || saved.Profiles[0].Strategy != "split-sni" || saved.Profiles[1].Strategy != c.Profiles[1].Strategy {
		t.Fatal("screening leaked into saved config")
	}
	last := job.Trials[len(job.Trials)-1]
	if !last.Passed || len(last.Results) != len(c.Profiles[0].Targets)*2*2*3 {
		t.Fatal("full TLS/IP/attempt matrix not confirmed")
	}
}

func TestLuaFastStartRespectsSavedBudgetsAndRegularCooldown(t *testing.T) {
	c := adaptiveFixture(t)
	if err := PrepareAdaptive(c); err != nil {
		t.Fatal(err)
	}
	runAdaptiveLua(t, c, luaPacketHarness+`
pilot_cooldown=30;pilot_fast_start=true;pilot_max_switches=4
local opts={first='none',third=true};local h12=hello(false)
local function fail(host) for i=1,2 do packet(host,true,false,h12,opts) end end
fail('fast.example.com')
assert(packet('fast.example.com',false,false,h12,opts)=='fake-md5')
-- The harness advances three seconds per packet. A second pair fails at +6/+9.
fail('fast.example.com')
assert(packet('fast.example.com',false,false,h12,opts)=='fake-md5','ten-second floor bypassed')
now=now+2
fail('fast.example.com')
assert(packet('fast.example.com',false,false,h12,opts)=='hostfake-md5','fast-start cooldown not shortened')
autostate=nil;dofile(PILOT_SCRIPT)
now=now+11;fail('fast.example.com')
assert(packet('fast.example.com',false,false,h12,opts)=='none','third switch not expedited after reload')
now=now+11;fail('fast.example.com')
assert(packet('fast.example.com',false,false,h12,opts)=='none','fourth switch ignored regular pause')
now=now+31;fail('fast.example.com')
assert(packet('fast.example.com',false,false,h12,opts)=='fake-md5')
now=now+40;fail('fast.example.com')
assert(packet('fast.example.com',false,false,h12,opts)=='fake-md5','fast start bypassed four-switch budget')
pilot_fast_start=false
fail('normal.example.com')
assert(packet('normal.example.com',false,false,h12,opts)=='fake-md5')
now=now+11;fail('normal.example.com')
assert(packet('normal.example.com',false,false,h12,opts)=='fake-md5','opt-out ignored configured pause')
`)
}

func TestLuaRecentOwnFailureLosesLearnedPriorityButKeepsFallback(t *testing.T) {
	c := adaptiveFixture(t)
	if err := PrepareAdaptive(c); err != nil {
		t.Fatal(err)
	}
	runAdaptiveLua(t, c, luaPacketHarness+`
local opts={first='none',third=true};local h12=hello(false)
local function fail(host) for i=1,2 do packet(host,true,false,h12,opts) end end
fail('donor.example.com');packet('donor.example.com',false,false,h12,opts)
fail('donor.example.com')
assert(packet('donor.example.com',false,true,h12,opts)=='hostfake-md5')
fail('target.example.com')
assert(packet('target.example.com',false,false,h12,opts)=='hostfake-md5')
fail('target.example.com')
assert(packet('target.example.com',false,false,h12,opts)=='fake-md5')
fail('target.example.com')
assert(packet('target.example.com',false,false,h12,opts)=='none')
fail('target.example.com')
assert(packet('target.example.com',false,false,h12,opts)=='fake-md5','recent own failure promoted from donor again')
fail('target.example.com')
assert(packet('target.example.com',false,false,h12,opts)=='hostfake-md5','history permanently removed a fallback')
`)
}
