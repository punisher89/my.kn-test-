package pilot

import (
	"slices"
	"strings"
	"testing"
)

func TestLegacyVLESSRollbackMatchesPreviouslyInstalledRules(t *testing.T) {
	previous := vlessRules{"192.168.1.1", []vlessDevice{{"192.168.3.50", "br2"}}}
	filter, jumps := previous.rules()
	if len(jumps) != 1 || strings.Join(jumps[0].Args, " ") != "-m comment --comment keenetic-pilot-vless -i br2 -s 192.168.3.50 -p tcp -m multiport --dports 80,443 -j PILOT_VLESS_V4" {
		t.Fatalf("old PREROUTING rule mismatch: %v", jumps)
	}
	if len(filter) != 2 || !slices.Contains(filter[0].Args, "DNAT") || !slices.Contains(filter[1].Args, "DROP") {
		t.Fatalf("old INPUT rules mismatch: %v", filter)
	}
}

func TestLocalHTTPSCertificatePersistsForLAN(t *testing.T) {
	dir := t.TempDir()
	a, err := PanelTLSConfig(dir, "192.168.1.1")
	if err != nil {
		t.Fatal(err)
	}
	b, err := PanelTLSConfig(dir, "192.168.1.1")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(a.Certificates[0].Certificate[0], b.Certificates[0].Certificate[0]) {
		t.Fatal("certificate changed across normal restarts")
	}
	if _, err := PanelTLSConfig(dir, "8.8.8.8"); err == nil {
		t.Fatal("accepted non-LAN IP")
	}
}
