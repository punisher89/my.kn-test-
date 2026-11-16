package pilot

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAdaptiveCountFrameValidationAndLegacyRead(t *testing.T) {
	r := AdaptiveHost{Group: "discovered_p443_tls13_ipv4", Host: "example.com", Strategy: "fake-md5", Status: "failed", Updated: 120, Symptom: "rst", Network: "ppp0", Port: 443, Origin: "pool", HoldReason: "none", EventCount: 12, FirstUpdated: 100}
	path := filepath.Join(t.TempDir(), "state.tsv")
	data := string(adaptiveFrame(4, nil, []AdaptiveHost{r}))
	check := func(data string) ([]AdaptiveHost, error) {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		_, _, events, err := readAdaptiveSlot(path)
		return events, err
	}
	got, err := check(data)
	if err != nil || len(got) != 1 || got[0].EventCount != 12 || got[0].FirstUpdated != 100 {
		t.Fatal("counters not restored", err)
	}
	for _, bad := range []string{strings.Replace(data, "\t12\t100\n", "\t0\t100\n", 1), strings.Replace(data, "\t12\t100\n", "\t12\t121\n", 1), strings.Replace(data, "\t12\t100\n", "\t1000000001\t100\n", 1), strings.Replace(data, "\tfailed\t", "\tselected\t", 1)} {
		if _, err := check(bad); err == nil {
			t.Fatal("bad counter frame accepted")
		}
	}
	row := strings.Split(strings.Split(data, "\n")[1], "\t")
	legacy3 := "PILOT3\t4\n" + strings.Join(row[:15], "\t") + "\nEND\t4\n"
	got, err = check(legacy3)
	if err != nil || got[0].EventCount != 1 || got[0].FirstUpdated != 120 {
		t.Fatal("legacy frame counters not defaulted", err)
	}
	legacy2 := "PILOT2\t4\n" + strings.Join(row[:10], "\t") + "\nEND\t4\n"
	if _, err = check(legacy2); err != nil {
		t.Fatal("PILOT2 rejected", err)
	}
}
func TestLuaFailureCountersPersistAcrossReloadAndKeepTransitions(t *testing.T) {
	c := adaptiveFixture(t)
	if err := PrepareAdaptive(c); err != nil {
		t.Fatal(err)
	}
	runAdaptiveLua(t, c, luaPacketHarness+`
pilot_cooldown=300;pilot_max_switches=1
local h=hello(true)
for i=1,45 do
  packet('noisy.example.com',true,false,h)
  if i%9==0 then packet('other.example.com',true,false,h) end
end
autostate=nil;dofile(PILOT_SCRIPT)
pilot_cooldown=300;pilot_max_switches=1
for i=1,12 do packet('noisy.example.com',true,false,h) end
packet('noisy.example.com',false,true,h)
packet('after.example.com',true,false,h)
`)
	got := ReadAdaptive(c)
	var repeated, selected, connected bool
	var total int64
	for _, r := range got.History {
		total += r.EventCount
		if r.EventCount > 1 {
			repeated = true
			if r.Status != "failed" || r.FirstUpdated > r.Updated {
				t.Fatal("invalid grouped failure")
			}
		}
		if r.Status == "selected" {
			selected = true
		}
		if r.Status == "connected" {
			connected = true
		}
	}
	if !repeated || !selected || !connected || total <= int64(len(got.History)) {
		t.Fatalf("counters or transitions lost: events=%+v", got.History)
	}
	report := NewManager(c, filepath.Join(t.TempDir(), "config.json"), &testBackend{}, successfulProber{}, &LogBuffer{}).Diagnostics()
	var exported int64
	for _, r := range report.History {
		exported += r.EventCount
	}
	if exported != total {
		t.Fatal("diagnostics lost persistent counters")
	}
}

func TestLuaMigratesPILOT3WithoutLosingMethodOrFailures(t *testing.T) {
	c := adaptiveFixture(t)
	if err := PrepareAdaptive(c); err != nil {
		t.Fatal(err)
	}
	group := "discovered_p443_tls13_ipv4"
	legacy := "PILOT3\t10\nR\t" + group + "\tlegacy.example.com\tfake-md5\tprobing\t900\trst\tppp0\t443\tpool\t0\t0\t0\t0\tnone\n"
	for _, stamp := range []string{"901", "902"} {
		legacy += "E\t" + group + "\tlegacy.example.com\tfake-md5\tfailed\t" + stamp + "\trst\tppp0\t443\tpool\t0\t0\t0\t0\tnone\n"
	}
	legacy += "END\t10\n"
	if err := os.WriteFile(filepath.Join(c.RuntimeDir, "state1.tsv"), []byte(legacy), 0600); err != nil {
		t.Fatal(err)
	}
	runAdaptiveLua(t, c, luaPacketHarness+`
assert(packet('legacy.example.com',false,true,hello(true))=='fake-md5','PILOT3 chosen method lost')
`)
	got := ReadAdaptive(c)
	var found bool
	for _, r := range got.History {
		if r.Status == "failed" && r.Host == "legacy.example.com" {
			found = true
			if r.EventCount != 2 || r.FirstUpdated != 901 || r.Updated != 902 {
				t.Fatal("old failures not compacted correctly")
			}
		}
	}
	if !found {
		t.Fatal("old failures lost")
	}
}
func TestDomainResetPreservesOtherFailureCounters(t *testing.T) {
	c := DefaultConfig()
	c.RuntimeDir = filepath.Join(t.TempDir(), "state")
	if err := PrepareAdaptive(c); err != nil {
		t.Fatal(err)
	}
	r := AdaptiveHost{Group: "discovered_p443_tls13_ipv4", Host: "keep.example.com", Strategy: "fake-md5", Status: "failed", Updated: 120, Symptom: "rst", Network: "ppp0", Port: 443, Origin: "pool", HoldReason: "none", EventCount: 12, FirstUpdated: 100}
	if err := os.WriteFile(filepath.Join(c.RuntimeDir, "state1.tsv"), adaptiveFrame(1, nil, []AdaptiveHost{r}), 0600); err != nil {
		t.Fatal(err)
	}
	m := NewManager(c, filepath.Join(t.TempDir(), "config.json"), &testBackend{}, successfulProber{}, &LogBuffer{})
	if err := m.ControlDomain(DomainAction{Host: "other.example.com", Action: "reset"}); err != nil {
		t.Fatal(err)
	}
	got := ReadAdaptive(c)
	if len(got.History) != 1 || got.History[0].EventCount != 12 || got.History[0].FirstUpdated != 100 {
		t.Fatal("reset lost unrelated failure counters")
	}
}
