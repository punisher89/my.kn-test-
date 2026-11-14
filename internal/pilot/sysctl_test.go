package pilot

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestFirmwareWithoutAccountingSysctl(t *testing.T) {
	dir := t.TempDir()
	liberalPath := filepath.Join(dir, "nf_conntrack_tcp_be_liberal")
	if err := os.WriteFile(liberalPath, []byte("0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	liberal, acct, err := readConntrackSettings(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(acct) != 0 {
		t.Fatal("missing knob has a fabricated original value")
	}
	if err = enableConntrackSettings(dir, liberal, acct); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(dir, "nf_conntrack_acct")); !os.IsNotExist(err) {
		t.Fatal("absent accounting knob created")
	}
	value, _ := os.ReadFile(liberalPath)
	if string(value) != "1" {
		t.Fatal("existing setting not enabled")
	}
	if err = writeExistingSysctl(liberalPath, liberal); err != nil {
		t.Fatal(err)
	}
	value, _ = os.ReadFile(liberalPath)
	if string(value) != "0" {
		t.Fatal("original setting not restored")
	}
	for _, rule := range FirewallRules(DefaultConfig()) {
		if slices.Contains(rule.Args, "NFQUEUE") && !slices.Contains(rule.Args, "--connbytes") {
			t.Fatal("packet window removed as fallback")
		}
	}
}

func TestMissingBothTunablesAndInvalidKnob(t *testing.T) {
	dir := t.TempDir()
	l, a, err := readConntrackSettings(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err = enableConntrackSettings(dir, l, a); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatal("missing knobs created")
	}
	path := filepath.Join(dir, "nf_conntrack_acct")
	if err = os.WriteFile(path, []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err = readConntrackSettings(dir); err == nil {
		t.Fatal("unexpected kernel value ignored")
	}
	if err = writeExistingSysctl(filepath.Join(dir, "missing"), []byte("1")); !os.IsNotExist(err) {
		t.Fatal("write fabricated missing file")
	}
}
