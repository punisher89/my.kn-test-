package pilot

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestVLESSRoutingSelectionMatchesOnlyChosenDevices(t *testing.T) {
	c := DefaultConfig()
	c.WAN = "ppp0"
	devices := []vlessDevice{{"192.168.1.44", "br0"}, {"192.168.3.50", "br2"}, {"10.0.0.2", "ppp0"}}
	selection := vlessSelection{Devices: []string{"192.168.3.50"}, Services: []string{"tmdb"}}
	selected, domains, err := validateVLESSSelection(selection, c, devices)
	if err != nil || len(selected) != 1 || !slices.Contains(domains, "domain:tmdb.org") {
		t.Fatalf("selection not resolved: %v %v %v", selected, domains, err)
	}
	filter, chain, jumps := (vlessRules{"192.168.1.1", selected}).rules()
	if len(jumps) != 1 || !slices.Contains(jumps[0].Args, "br2") || !slices.Contains(jumps[0].Args, "192.168.3.50") ||
		len(filter) != 2 || !slices.Contains(filter[0].Args, "DNAT") || !slices.Contains(filter[1].Args, "DROP") {
		t.Fatalf("rules outside selection: %v, %v", filter, jumps)
	}
	if got := strings.Join(chain[len(chain)-1].Args, " "); !strings.HasSuffix(got, "DNAT --to-destination 192.168.1.1:10892") {
		t.Fatal(got)
	}
	selection.Devices = []string{"10.0.0.2"}
	if _, _, err := validateVLESSSelection(selection, c, devices); err == nil {
		t.Fatal("WAN device accepted")
	}
}

func TestVLESSRoutingXrayConfig(t *testing.T) {
	cfg, err := vlessConfig(realityTestLink())
	if err != nil {
		t.Fatal(err)
	}
	if err := addVLESSRouting(cfg, "192.168.1.1", []string{"domain:tmdb.org"}); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(cfg)
	if err != nil || !strings.Contains(string(data), `"followRedirect":true`) ||
		!strings.Contains(string(data), `"domain":["domain:tmdb.org"],"inboundTag":["pilot-lan"],"outboundTag":"vless"`) ||
		!strings.Contains(string(data), `"protocol":"freedom","tag":"direct"`) {
		t.Fatal("routes or fallback missing", err)
	}
	if os.Getenv("PILOT_TEST_XRAY") != "" {
		file := filepath.Join(t.TempDir(), "test.json")
		if err := os.WriteFile(file, data, 0600); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command("xray", "run", "-test", "-config", file).CombinedOutput(); err != nil {
			t.Fatalf("Xray rejected routing: %v: %s", err, out)
		}
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
