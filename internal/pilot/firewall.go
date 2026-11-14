package pilot

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strconv"
	"time"
)

type Rule struct {
	Tool  string
	Table string
	Chain string
	Args  []string
}

type Runner func(string, ...string) error

var ErrRuleAbsent = errors.New("firewall rule absent")

func RunCommand(name string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		var exit *exec.ExitError
		if slices.Contains(args, "-C") && errors.As(err, &exit) && exit.ExitCode() == 1 {
			return ErrRuleAbsent
		}
		if len(out) > 1024 {
			out = out[:1024]
		}
		return fmt.Errorf("%s: %w: %s", name, err, out)
	}
	return nil
}

func FirewallRules(c Config) []Rule {
	rules := []Rule{}
	tools := []string{"iptables"}
	if c.IPv6 {
		tools = append(tools, "ip6tables")
	}
	for _, tool := range tools {
		add := func(table, chain string, a ...string) { rules = append(rules, Rule{tool, table, chain, a}) }
		ports := "80,443,2053,2083,2087,2096,8443"
		// Keep the handshake visible on Keenetic MediaTek. The algorithm is
		// described in z2k's z2k-ppe-deoffload.sh (see THIRD_PARTY.md).
		if c.PPE {
			for _, chain := range []string{"PREROUTING", "FORWARD"} {
				add("mangle", chain, "-p", "tcp", "-m", "multiport", "--dports", ports, "-m", "connskip", "--connskip", "30", "-m", "comment", "--comment", "keenetic-pilot", "-j", "PPE")
				if c.QUIC || c.DiscordVoice {
					udp := "443"
					if c.DiscordVoice {
						udp += ",50000:50099"
					}
					add("mangle", chain, "-p", "udp", "-m", "multiport", "--dports", udp, "-m", "connskip", "--connskip", "30", "-m", "comment", "--comment", "keenetic-pilot", "-j", "PPE")
				}
			}
		}
		for _, dir := range []struct{ chain, iface, port, ctdir string }{{"POSTROUTING", "-o", "--dports", "original"}, {"PREROUTING", "-i", "--sports", "reply"}} {
			for _, proto := range []string{"tcp", "udp"} {
				if proto == "udp" && !c.QUIC && !c.DiscordVoice {
					continue
				}
				pp := ports
				if proto == "udp" {
					pp = "443"
					if c.DiscordVoice {
						pp += ",50000:50099"
					}
				}
				add("mangle", dir.chain, dir.iface, c.WAN, "-p", proto, "-m", "multiport", dir.port, pp, "-m", "mark", "!", "--mark", "0x40000000/0x40000000", "-m", "connbytes", "--connbytes", "1:20", "--connbytes-dir", dir.ctdir, "--connbytes-mode", "packets", "-m", "comment", "--comment", "keenetic-pilot", "-j", "NFQUEUE", "--queue-num", strconv.Itoa(c.Queue), "--queue-bypass")
			}
		}
		// Generated packets must not be interpreted by conntrack as originals.
		add("raw", "OUTPUT", "-m", "mark", "--mark", "0x40000000/0x40000000", "-m", "comment", "--comment", "keenetic-pilot", "-j", "CT", "--notrack")
	}
	return rules
}

func (r Rule) command(op string) []string {
	// Entware iptables 1.4.21 accepts -w but not a following timeout value.
	// RunCommand bounds the whole process, including lock waiting, to 10s.
	a := []string{"-w", "-t", r.Table, op, r.Chain}
	if op == "-I" {
		a = append(a, "1")
	}
	return append(a, r.Args...)
}

func EnsureRules(run Runner, rules []Rule) error {
	// Inserting at position 1 reverses order, so visit backwards. PPE must
	// execute before NFQUEUE on the same PREROUTING chain.
	for i := len(rules) - 1; i >= 0; i-- {
		r := rules[i]
		err := run(r.Tool, r.command("-C")...)
		if err == nil {
			continue
		}
		if !errors.Is(err, ErrRuleAbsent) {
			return err
		}
		if err := run(r.Tool, r.command("-I")...); err != nil {
			return err
		}
	}
	return nil
}

func RemoveRules(run Runner, rules []Rule) error {
	var first error
	for i := len(rules) - 1; i >= 0; i-- {
		r := rules[i]
		for n := 0; n < 8; n++ {
			err := run(r.Tool, r.command("-C")...)
			if errors.Is(err, ErrRuleAbsent) {
				break
			}
			if err != nil {
				if first == nil {
					first = err
				}
				break
			}
			if err := run(r.Tool, r.command("-D")...); err != nil {
				if first == nil {
					first = err
				}
				break
			}
		}
	}
	return first
}
