package pilot

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
)

const (
	vlessRoutePort = "10892"
	vlessChain     = "PILOT_VLESS_V4"
	vlessComment   = "keenetic-pilot-vless"
)

type vlessDevice struct {
	IP        string
	Interface string
}

func vlessBridge(name string) bool {
	return interfaceRE.MatchString(name) && (strings.HasPrefix(strings.ToLower(name), "br") || name == "Home")
}

type vlessRules struct {
	LAN     string
	Devices []vlessDevice
}

func (v vlessRules) journal(dir string) string { return filepath.Join(dir, "vless-firewall.json") }

func (v vlessRules) rules() (filter, jumps []Rule) {
	rule := func(table, chain string, args ...string) Rule {
		return Rule{"iptables", table, chain, append([]string{"-m", "comment", "--comment", vlessComment}, args...)}
	}
	for _, d := range v.Devices {
		filter = append(filter, rule("filter", "INPUT", "-i", d.Interface, "-s", d.IP, "-d", v.LAN,
			"-p", "tcp", "--dport", vlessRoutePort, "-m", "conntrack", "--ctstate", "DNAT", "-j", "ACCEPT"))
		jumps = append(jumps, rule("nat", "PREROUTING", "-i", d.Interface, "-s", d.IP, "-p", "tcp", "-m", "multiport", "--dports", "80,443", "-j", vlessChain))
	}
	filter = append(filter, rule("filter", "INPUT", "-d", v.LAN, "-p", "tcp", "--dport", vlessRoutePort, "-j", "DROP"))
	return
}

func (v vlessRules) remove(dir string) error {
	filter, jumps := v.rules()
	if err := RemoveRules(RunCommand, jumps); err != nil {
		return err
	}
	if err := RemoveRules(RunCommand, filter); err != nil {
		return err
	}
	// The private chain can be missing after reboot, with the journal left on disk.
	if RunCommand("iptables", "-w", "-t", "nat", "-S", vlessChain) == nil {
		if err := RunCommand("iptables", "-w", "-t", "nat", "-F", vlessChain); err != nil {
			return err
		}
		if err := RunCommand("iptables", "-w", "-t", "nat", "-X", vlessChain); err != nil {
			return err
		}
	}
	if err := os.Remove(v.journal(dir)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func cleanupVLESSRoutes(dir string) error {
	v := vlessRules{}
	data, err := os.ReadFile(v.journal(dir))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	lan, err := netip.ParseAddr(v.LAN)
	if err != nil || !lan.Is4() || !lan.IsPrivate() || len(v.Devices) < 1 || len(v.Devices) > 8 {
		return errors.New("неверный журнал правил VLESS")
	}
	for _, d := range v.Devices {
		ip, err := netip.ParseAddr(d.IP)
		if err != nil || !ip.Is4() || !ip.IsPrivate() || !vlessBridge(d.Interface) {
			return fmt.Errorf("неверное устройство в журнале правил VLESS")
		}
	}
	return v.remove(dir)
}
