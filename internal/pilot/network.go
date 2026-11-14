package pilot

import (
	"context"
	"net"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

type NetworkInterface struct {
	Name      string   `json:"name"`
	Addresses []string `json:"addresses"`
}
type NetworkInfo struct {
	WAN        string             `json:"wan"`
	LAN        string             `json:"lan"`
	Listen     string             `json:"listen"`
	Interfaces []NetworkInterface `json:"interfaces"`
}

func routeInterface(output string) string {
	f := strings.Fields(output)
	for i := 0; i+1 < len(f); i++ {
		if f[i] == "dev" && interfaceRE.MatchString(f[i+1]) {
			return f[i+1]
		}
	}
	return ""
}

func chooseLAN(interfaces []NetworkInterface, wan string) string {
	type choice struct {
		ip    string
		score int
	}
	candidates := []choice{}
	for _, iface := range interfaces {
		if iface.Name == wan || iface.Name == "lo" {
			continue
		}
		for _, addr := range iface.Addresses {
			ip := net.ParseIP(addr)
			if ip == nil || ip.To4() == nil || !ip.IsPrivate() {
				continue
			}
			score := 1
			switch iface.Name {
			case "br0", "Bridge0", "Home":
				score = 100
			default:
				if strings.HasPrefix(strings.ToLower(iface.Name), "br") {
					score = 50
				}
			}
			candidates = append(candidates, choice{addr, score})
		}
	}
	if len(candidates) == 0 {
		return ""
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].score > candidates[j].score })
	if len(candidates) > 1 && candidates[0].score == candidates[1].score && candidates[0].ip != candidates[1].ip {
		return ""
	}
	return candidates[0].ip
}

func DetectNetwork() NetworkInfo {
	info := NetworkInfo{Interfaces: []NetworkInterface{}}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	// This is a local route lookup, not a request to the address.
	if out, err := exec.CommandContext(ctx, "ip", "-4", "route", "get", "1.1.1.1").Output(); err == nil {
		info.WAN = routeInterface(string(out))
	}
	interfaces, _ := net.Interfaces()
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		v := NetworkInterface{Name: iface.Name, Addresses: []string{}}
		addrs, _ := iface.Addrs()
		for _, addr := range addrs {
			if ip, _, err := net.ParseCIDR(addr.String()); err == nil {
				v.Addresses = append(v.Addresses, ip.String())
			}
		}
		info.Interfaces = append(info.Interfaces, v)
	}
	info.LAN = chooseLAN(info.Interfaces, info.WAN)
	if info.LAN != "" {
		for port := 9080; port <= 9090; port++ {
			address := net.JoinHostPort(info.LAN, strconv.Itoa(port))
			listener, err := net.Listen("tcp4", address)
			if err == nil {
				listener.Close()
				info.Listen = address
				break
			}
		}
	}
	return info
}
