package pilot

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestFirewallOrderIdempotenceAndExactRemoval(t *testing.T) {
	c := DefaultConfig()
	c.WAN = "ppp0"
	c.IPv6 = true
	rules := FirewallRules(c)
	store := map[string]bool{}
	inserts := []string{}
	deleted := 0
	run := func(tool string, args ...string) error {
		op := ""
		idx := 0
		for i, s := range args {
			if s == "-C" || s == "-I" || s == "-D" {
				op = s
				idx = i
				break
			}
		}
		normal := append([]string{}, args...)
		normal[idx] = "RULE"
		if op == "-I" {
			normal = append(normal[:idx+2], normal[idx+3:]...)
		}
		key := tool + " " + strings.Join(normal, " ")
		switch op {
		case "-C":
			if !store[key] {
				return ErrRuleAbsent
			}
		case "-I":
			store[key] = true
			inserts = append(inserts, key)
		case "-D":
			delete(store, key)
			deleted++
		}
		return nil
	}
	if err := EnsureRules(run, rules); err != nil {
		t.Fatal(err)
	}
	count := len(inserts)
	if err := EnsureRules(run, rules); err != nil {
		t.Fatal(err)
	}
	if len(inserts) != count {
		t.Fatal("duplicate rules inserted")
	}
	// Each new rule is inserted at chain position 1, so PPE insertion happens
	// after queue insertion and consequently executes before the queue.
	queue, ppe := -1, -1
	for i, s := range inserts {
		if strings.HasPrefix(s, "iptables ") && strings.Contains(s, "PREROUTING") {
			if strings.Contains(s, "NFQUEUE") {
				queue = i
			}
			if strings.Contains(s, "-j PPE") {
				ppe = i
			}
		}
	}
	if queue < 0 || ppe <= queue {
		t.Fatal("PPE would not precede NFQUEUE")
	}
	if err := RemoveRules(run, rules); err != nil {
		t.Fatal(err)
	}
	if len(store) != 0 || deleted != len(rules) {
		t.Fatal("own rules not removed exactly")
	}
	for _, r := range rules {
		if !slices.Contains(r.Args, "keenetic-pilot") {
			t.Fatal("untagged rule")
		}
		if slices.Contains(r.Args, "NFQUEUE") && !slices.Contains(r.Args, "--queue-bypass") {
			t.Fatal("queue is not fail-open")
		}
	}
}

func TestLegacyIptablesWaitSyntaxThroughRealRunner(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	// This executable reproduces the old parser: -w is a flag, its next
	// argument must be another option, not a numeric lock timeout.
	script := "#!/bin/sh\n[ \"$1\" = -w ] || exit 2\nshift\ncase \"$1\" in [0-9]*) echo \"Bad argument timeout\" >&2; exit 2;; esac\nprintf '%s\\n' \"$*\" >> \"$PILOT_LEGACY_LOG\"\nfor a in \"$@\"; do [ \"$a\" != -C ] || exit 1; done\nexit 0\n"
	for _, tool := range []string{"iptables", "ip6tables"} {
		if err := os.WriteFile(filepath.Join(dir, tool), []byte(script), 0755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("PILOT_LEGACY_LOG", log)
	c := DefaultConfig()
	c.WAN = "ppp0"
	c.IPv6 = true
	if err := EnsureRules(RunCommand, FirewallRules(c)); err != nil {
		t.Fatal(err)
	}
	if err := RemoveRules(RunCommand, FirewallRules(c)); err != nil {
		t.Fatal(err)
	}
	if err := RunCommand("iptables", "-w", "-t", "mangle", "-S"); err != nil {
		t.Fatal(err)
	}
	if err := RunCommand("iptables", "-w", "5", "-t", "mangle", "-S"); err == nil {
		t.Fatal("legacy parser fixture does not reject numeric wait")
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "-I") || !strings.Contains(string(data), "-C") {
		t.Fatal("rule operations did not execute")
	}
}

func TestFirewallErrorsAreNotMistakenForMissingRules(t *testing.T) {
	fault := errors.New("xtables locked or unavailable")
	mutated := false
	run := func(_ string, args ...string) error {
		if !slices.Contains(args, "-C") {
			mutated = true
		}
		return fault
	}
	rules := FirewallRules(DefaultConfig())
	if err := EnsureRules(run, rules); !errors.Is(err, fault) {
		t.Fatal("check failure ignored")
	}
	if err := RemoveRules(run, rules); !errors.Is(err, fault) {
		t.Fatal("cleanup failure ignored")
	}
	if mutated {
		t.Fatal("firewall mutated after prerequisite check failed")
	}
}
