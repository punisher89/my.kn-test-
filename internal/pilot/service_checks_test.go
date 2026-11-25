package pilot

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

func TestServiceCheckReadOnlyReportsAndCooldown(t *testing.T) {
	c := DefaultConfig()
	c.Enabled = true
	c.WAN = "ppp0"
	b := &testBackend{running: true}
	m := NewManager(c, filepath.Join(t.TempDir(), "config.json"), b, successfulProber{}, &LogBuffer{})
	before, _ := json.Marshal(m.Config())
	r, err := m.Check(context.Background(), "youtube")
	if err != nil || len(r) == 0 {
		t.Fatal(err)
	}
	report := m.Snapshot().Checks["youtube"]
	if !report.Complete || !report.Passed || report.Stale || !report.Running {
		t.Fatalf("bad report %+v", report)
	}
	r[0].Error = "caller mutation"
	if m.Snapshot().Checks["youtube"].Results[0].Error != "" {
		t.Fatal("caller mutated stored report")
	}
	report.Results[0].Error = "snapshot mutation"
	if m.Snapshot().Checks["youtube"].Results[0].Error != "" {
		t.Fatal("snapshot mutated stored report")
	}
	if _, err = m.Check(context.Background(), "youtube"); err == nil {
		t.Fatal("manual probe flooding allowed")
	}
	if len(b.configs) != 0 {
		t.Fatal("check restarted backend")
	}
	after, _ := json.Marshal(m.Config())
	if string(before) != string(after) {
		t.Fatal("check changed config")
	}
	m.mu.Lock()
	old := m.checks["youtube"]
	old.Finished = time.Now().Add(-serviceCheckFreshness - time.Second)
	m.checks["youtube"] = old
	m.mu.Unlock()
	if !m.Snapshot().Checks["youtube"].Stale {
		t.Fatal("expired check still shown as fresh")
	}
	m.mu.Lock()
	old.Finished = time.Now()
	m.checks["youtube"] = old
	m.c.Profiles[0].Strategy = "none"
	m.mu.Unlock()
	if !m.Snapshot().Checks["youtube"].Stale {
		t.Fatal("changed method left green report")
	}
	m.op.Lock()
	_, err = m.Check(context.Background(), "discord")
	m.op.Unlock()
	if err != ErrBusy {
		t.Fatal("check bypassed operation lock")
	}
}

func TestServiceCheckPartialAndPressure(t *testing.T) {
	m := NewManager(DefaultConfig(), filepath.Join(t.TempDir(), "config.json"), &testBackend{}, failedProber{}, &LogBuffer{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := m.Check(ctx, "youtube"); err != nil {
		t.Fatal(err)
	}
	report := m.Snapshot().Checks["youtube"]
	if report.Complete || report.Passed || report.Error == "" {
		t.Fatal("partial check shown as success")
	}
	cpu := 90.0
	mem := uint64(8 << 20)
	for _, d := range []DeviceInfo{{CPUPercent: &cpu}, {MemoryAvailable: &mem}, {CPUCores: 2, Load: []float64{3}}} {
		if backgroundPressure(d) == "" {
			t.Fatal("missed pressure", d)
		}
	}
	if backgroundPressure(DeviceInfo{}) != "" {
		t.Fatal("invented pressure without sensors")
	}
	cpu = 20
	mem = 128 << 20
	if backgroundPressure(DeviceInfo{CPUPercent: &cpu, MemoryAvailable: &mem, MemoryTotal: 256 << 20, CPUCores: 4, Load: []float64{0.5}}) != "" {
		t.Fatal("blocked healthy device")
	}
	if checkCooldown(ServiceCheck{Finished: time.Now().Add(-21 * time.Second)}) != nil {
		t.Fatal("cooldown did not expire")
	}
}

func TestLuaGlobalSwitchBudgetAcrossHostsAndContexts(t *testing.T) {
	c := adaptiveFixture(t)
	if err := PrepareAdaptive(c); err != nil {
		t.Fatal(err)
	}
	runAdaptiveLua(t, c, luaPacketHarness+`
pilot_global_max_switches=2
local base={first='none',third=true};local h12=hello(false);local h13=hello(true)
for _,host in ipairs({'one.example.com','two.example.com'}) do
 for i=1,2 do packet(host,true,false,h12,base) end
 assert(packet(host,false,false,h12,base)=='fake-md5')
end
for i=1,2 do packet('three.example.com',true,false,h13,base) end
assert(packet('three.example.com',false,false,h13,base)=='none','global budget failed across TLS contexts')
now=now+601
for i=1,2 do packet('three.example.com',true,false,h13,base) end
assert(packet('three.example.com',false,false,h13,base)=='fake-md5','global budget did not recover')
`)
	got := ReadAdaptive(c)
	found := false
	for _, r := range got.History {
		if r.Host == "three.example.com" && r.Status == "held" && r.HoldReason == "limit" {
			found = true
		}
	}
	if !found {
		t.Fatal("shared budget pause missing from history")
	}
}

func TestBackgroundGuardThrottlesSnapshotLikeHighLoad(t *testing.T) {
    highCPU := 68.94273127753303
    underCPU := 40.0
    mem := uint64(185233408)
    d:=DeviceInfo{
        CPUPercent: &highCPU, CPUCores: 2, Load: []float64{2.46,2.46,2.21},
        MemoryTotal: 512946176, MemoryAvailable: &mem,
    }
    if got:=backgroundPressure(d);got=="" {t.Fatal("router load 2.46 on 2 cores was not throttled")}
    d.CPUPercent=&underCPU
    if got:=backgroundPressure(d);got=="" {t.Fatal("2-core 2.46 load escaped guard")}
    d.Load=[]float64{0.4}
    if got:=backgroundPressure(d);got!="" {t.Fatalf("healthy router unnecessarily held: %s",got)}
}
