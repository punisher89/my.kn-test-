package pilot

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBoxesKeepEvidenceContextsSeparateAndIgnoreSupersededSuccess(t *testing.T) {
	c := DefaultConfig()
	c.WAN = "ppp0"
	row := AdaptiveHost{Group: "discovered_p443_tls12_ipv4", Host: "a.example.com", Strategy: "hostfake-md5", Status: "connected", Updated: 1000, Symptom: "rst", Network: "ppp0", Port: 443, Origin: "pool"}
	rows := []AdaptiveHost{row}
	for i, edit := range []func(*AdaptiveHost){
		func(r *AdaptiveHost) { r.Group = "discovered_p443_tls13_ipv4" },
		func(r *AdaptiveHost) { r.Group = "discovered_p443_tls12_ipv6" },
		func(r *AdaptiveHost) { r.Port = 8443 },
		func(r *AdaptiveHost) { r.Network = "ppp1" },
		func(r *AdaptiveHost) { r.Symptom = "tls_alert" },
	} {
		r := row
		edit(&r)
		r.Host = []string{"tls.example.com", "ipv6.example.com", "port.example.com", "wan.example.com", "alert.example.com"}[i]
		rows = append(rows, r)
	}
	// A later failed observation of the same host under a changed profile must
	// invalidate the older success for experience reuse, even with equal clocks.
	failed := row
	failed.Group = "youtube_split-sni_p443_tls12_ipv4"
	failed.Status = "probing"
	failed.Strategy = "fake-md5"
	rows = append(rows, failed)
	boxes := adaptiveBoxes(rows, []AdaptiveHost{}, c, 1001)
	if len(boxes) != 6 {
		t.Fatalf("incompatible contexts merged: %d boxes", len(boxes))
	}
	for _, b := range boxes {
		for _, r := range b.Hosts {
			if r.Host == row.Host && (r.Status != "probing" || len(b.Methods) != 0) {
				t.Fatal("old success hid newer failure")
			}
		}
		for _, m := range b.Methods {
			if b.Network == "ppp1" && m.Reusable {
				t.Fatal("foreign WAN shown as reusable")
			}
		}
	}
	stale := adaptiveBoxes([]AdaptiveHost{row}, nil, c, 1000+8*24*60*60)
	if len(stale[0].Methods) != 1 || stale[0].Methods[0].Reusable {
		t.Fatal("stale evidence became a reusable candidate")
	}
	// Stable IDs let users keep a card open across refreshes.
	again := adaptiveBoxes([]AdaptiveHost{row}, nil, c, 1001)
	if stale[0].ID != again[0].ID {
		t.Fatal("box ID depends on time or status")
	}
}

func TestLegacyAdaptiveChoicesMigrateWithoutLosingSelection(t *testing.T) {
	c := adaptiveFixture(t)
	if err := PrepareAdaptive(c); err != nil {
		t.Fatal(err)
	}
	old := "PILOT1\t5\ndiscovered_tls12_ipv4\tlegacy.example.com\tfake-md5\tconnected\t900\nEND\t5\n"
	if err := os.WriteFile(filepath.Join(c.RuntimeDir, "state1.tsv"), []byte(old), 0600); err != nil {
		t.Fatal(err)
	}
	runAdaptiveLua(t, c, luaPacketHarness+`
local opts={first='none',third=true}
assert(packet('legacy.example.com',false,false,hello(false),opts)=='fake-md5','upgrade changed saved choice')
assert(packet('legacy.example.com',false,true,hello(false),opts)=='fake-md5')
`)
	got := ReadAdaptive(c)
	if len(got.Hosts) != 1 || got.Hosts[0].Strategy != "fake-md5" || got.Hosts[0].Origin != "restored" || got.Generation <= 5 {
		t.Fatalf("legacy migration lost state: %+v", got)
	}
	if len(got.Boxes) != 1 || len(got.Boxes[0].Methods) != 1 || got.Boxes[0].Methods[0].Reusable {
		t.Fatal("old observation with unknown symptom used as box evidence")
	}
}
