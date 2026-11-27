package pilot

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

const (
	vlessRoutePort = "10892"
	vlessChain     = "PILOT_VLESS_V4"
	vlessComment   = "keenetic-pilot-vless"
)

type vlessSelection struct {
	Devices  []string `json:"devices"`
	Services []string `json:"services"`
}

type vlessDevice struct {
	IP        string `json:"ip"`
	Interface string `json:"interface"`
}

func vlessServiceCatalog() []GeoService {
	return append(slices.Clone(geoServices), GeoService{
		ID: "tmdb", Name: "TMDB / Seerr", Domains: []string{"themoviedb.org", "tmdb.org", "tmdb-image-prod.b-cdn.net"},
	})
}

func vlessLANDevices(c Config) []vlessDevice {
	out := []vlessDevice{}
	f, err := os.Open("/proc/net/arp")
	if err != nil {
		return out
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	for s.Scan() && len(out) < 128 {
		row := strings.Fields(s.Text())
		if len(row) != 6 || row[5] == c.WAN || !vlessBridge(row[5]) {
			continue
		}
		flags, err := strconv.ParseUint(row[2], 0, 32)
		if err != nil || flags&0x2 == 0 {
			continue
		}
		if ip, err := netip.ParseAddr(row[0]); err == nil && ip.Is4() && ip.IsPrivate() {
			out = append(out, vlessDevice{ip.String(), row[5]})
		}
	}
	return out
}

func vlessBridge(name string) bool {
	return interfaceRE.MatchString(name) && (strings.HasPrefix(strings.ToLower(name), "br") || name == "Home")
}

func validateVLESSSelection(s vlessSelection, c Config, inventory []vlessDevice) ([]vlessDevice, []string, error) {
	if len(s.Devices) > 8 || len(s.Services) > len(vlessServiceCatalog()) {
		return nil, nil, errors.New("выберите до восьми устройств и сервисы из списка")
	}
	if len(s.Devices) == 0 && len(s.Services) == 0 {
		return nil, nil, nil
	}
	if len(s.Devices) == 0 || len(s.Services) == 0 {
		return nil, nil, errors.New("выберите хотя бы одно устройство и один сервис")
	}
	selected := []vlessDevice{}
	for _, ip := range s.Devices {
		index := slices.IndexFunc(inventory, func(d vlessDevice) bool { return d.IP == ip && d.Interface != c.WAN })
		if index < 0 || slices.Contains(selected, inventory[index]) {
			return nil, nil, errors.New("устройство отсутствует в списке LAN или выбрано дважды. Проверьте, что оно в сети")
		}
		selected = append(selected, inventory[index])
	}
	domains := []string{}
	for _, id := range s.Services {
		catalog := vlessServiceCatalog()
		index := slices.IndexFunc(catalog, func(g GeoService) bool { return g.ID == id })
		if index < 0 {
			return nil, nil, errors.New("неизвестный сервис")
		}
		for _, domain := range catalog[index].Domains {
			rule := "domain:" + domain
			if !slices.Contains(domains, rule) {
				domains = append(domains, rule)
			}
		}
	}
	return selected, domains, nil
}

func vlessSelectionPath(dir string) string { return filepath.Join(dir, "vless-routing.json") }

func loadVLESSSelection(dir string) (vlessSelection, error) {
	var s vlessSelection
	data, err := os.ReadFile(vlessSelectionPath(dir))
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	err = json.Unmarshal(data, &s)
	return s, err
}

func addVLESSRouting(cfg map[string]any, lan string, domains []string) error {
	out, ok := cfg["outbounds"].([]any)
	in, inOK := cfg["inbounds"].([]any)
	if !ok || !inOK || len(out) != 1 || len(in) != 1 {
		return errors.New("VLESS: повреждён локальный профиль")
	}
	xray, outOK := out[0].(map[string]any)
	socks, socksOK := in[0].(map[string]any)
	if !outOK || !socksOK {
		return errors.New("VLESS: повреждён локальный профиль")
	}
	xray["tag"], socks["tag"] = "vless", "pilot-socks"
	cfg["outbounds"] = append(out, map[string]any{"tag": "direct", "protocol": "freedom"})
	cfg["inbounds"] = append(in, map[string]any{
		"tag": "pilot-lan", "listen": lan, "port": 10892, "protocol": "dokodemo-door",
		"settings": map[string]any{"network": "tcp", "followRedirect": true},
		"sniffing": map[string]any{"enabled": true, "destOverride": []string{"http", "tls"}, "routeOnly": true},
	})
	cfg["routing"] = map[string]any{"domainStrategy": "AsIs", "rules": []any{
		map[string]any{"type": "field", "inboundTag": []string{"pilot-lan"}, "domain": domains, "outboundTag": "vless"},
		map[string]any{"type": "field", "inboundTag": []string{"pilot-lan"}, "outboundTag": "direct"},
	}}
	return nil
}

type vlessRules struct {
	LAN     string
	Devices []vlessDevice
}

func (v vlessRules) journal(dir string) string { return filepath.Join(dir, "vless-firewall.json") }

func (v vlessRules) rules() (filter, chain, jumps []Rule) {
	rule := func(table, chain string, args ...string) Rule {
		return Rule{"iptables", table, chain, append([]string{"-m", "comment", "--comment", vlessComment}, args...)}
	}
	for _, d := range v.Devices {
		filter = append(filter, rule("filter", "INPUT", "-i", d.Interface, "-s", d.IP, "-d", v.LAN,
			"-p", "tcp", "--dport", vlessRoutePort, "-m", "conntrack", "--ctstate", "DNAT", "-j", "ACCEPT"))
		jumps = append(jumps, rule("nat", "PREROUTING", "-i", d.Interface, "-s", d.IP, "-p", "tcp", "-m", "multiport", "--dports", "80,443", "-j", vlessChain))
	}
	filter = append(filter, rule("filter", "INPUT", "-d", v.LAN, "-p", "tcp", "--dport", vlessRoutePort, "-j", "DROP"))
	for _, prefix := range []string{"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16", "172.16.0.0/12", "192.168.0.0/16", "224.0.0.0/3"} {
		chain = append(chain, rule("nat", vlessChain, "-d", prefix, "-j", "RETURN"))
	}
	chain = append(chain, rule("nat", vlessChain, "-p", "tcp", "-m", "addrtype", "!", "--dst-type", "LOCAL", "-j", "DNAT", "--to-destination", net.JoinHostPort(v.LAN, vlessRoutePort)))
	return
}

func (v vlessRules) prepare(dir string) error {
	if err := WriteJSON(v.journal(dir), v); err != nil {
		return err
	}
	filter, chain, _ := v.rules()
	if err := RunCommand("iptables", "-w", "-t", "nat", "-N", vlessChain); err != nil {
		_ = os.Remove(v.journal(dir))
		return err
	}
	for _, rules := range [][]Rule{filter, chain} {
		if err := EnsureRules(RunCommand, rules); err != nil {
			return errors.Join(err, v.remove(dir))
		}
	}
	return nil
}

func (v vlessRules) remove(dir string) error {
	filter, _, jumps := v.rules()
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
