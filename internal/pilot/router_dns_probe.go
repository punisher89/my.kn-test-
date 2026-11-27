package pilot

import (
	"bufio"
	"context"
	"errors"
	"net"
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Configuration inventory is distinct from live DNS answers. Only supported
// global DNS-proxy upstream entries are shown; no full configuration or URL
// paths, credentials, query strings, or filter profile secrets leave this code.
func configuredDNS(raw string) []secureDNSResult {
	var out []secureDNSResult
	seen := map[string]bool{}
	block := false
	scanner := bufio.NewScanner(strings.NewReader(raw))
	scanner.Buffer(make([]byte, 4096), 256<<10)
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if fields[0] == "dns-proxy" {
			block = len(fields) == 1
			fields = fields[1:]
		} else if !block || len(line) == len(strings.TrimLeft(line, " \t")) || fields[0] == "exit" {
			block = false
			continue
		}
		if len(fields) < 3 || fields[1] != "upstream" {
			continue
		}
		var protocol, server string
		endpoint := strings.Trim(fields[2], "\"'")
		switch fields[0] {
		case "https":
			u, e := url.Parse(endpoint)
			if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
				continue
			}
			protocol, server = "DoH", u.Hostname()
		case "tls", "quic", "doq":
			protocol = "DoT"
			if fields[0] != "tls" {
				protocol = "DoQ"
			}
			server = endpoint
			for i := 3; i+1 < len(fields); i++ {
				if fields[i] == "sni" {
					server = strings.Trim(fields[i+1], "\"'")
					break
				}
			}
		default:
			continue
		}
		if net.ParseIP(server) == nil {
			if len(server) > 253 {
				continue
			}
			valid := true
			for _, l := range strings.Split(strings.TrimSuffix(server, "."), ".") {
				if !dnsProbeLabel.MatchString(l) {
					valid = false
					break
				}
			}
			if !valid {
				continue
			}
		}
		key := protocol + " " + server
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, secureDNSResult{Protocol: protocol, Server: server, Status: "configured", Error: "Настроен в KeeneticOS; использование и доступность отдельно не подтверждены"})
		if len(out) >= 32 {
			break
		}
	}
	return out
}

// KeeneticOS reports effective proxy targets as "dns_server = IP:port .".
// Encrypted proxies can appear here as internal listeners, so keep these
// separate from the configured names and from a live lookup.
func proxyDNSMembers(raw string) []secureDNSResult {
	var rows []secureDNSResult
	seen := map[netip.AddrPort]bool{}
	scanner := bufio.NewScanner(strings.NewReader(raw))
	scanner.Buffer(make([]byte, 4096), 256<<10)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 3 || fields[0] != "dns_server" || fields[1] != "=" {
			continue
		}
		endpoint, err := netip.ParseAddrPort(fields[2])
		if err != nil || !endpoint.Addr().IsValid() || endpoint.Addr().IsUnspecified() || endpoint.Addr().IsMulticast() || endpoint.Port() == 0 || seen[endpoint] {
			continue
		}
		seen[endpoint] = true
		rows = append(rows, secureDNSResult{Protocol: "Прокси KeeneticOS", Server: endpoint.String(), Status: "configured", Error: "Резолвер из текущего состояния DNS-прокси"})
		if len(rows) == 32 {
			break
		}
	}
	return rows
}

func readRouterDNSConfig(ctx context.Context) []secureDNSResult {
	path, err := exec.LookPath("ndmc")
	if err != nil {
		for _, p := range []string{"/usr/bin/ndmc", "/bin/ndmc", "/sbin/ndmc", "/usr/sbin/ndmc"} {
			if st, e := os.Stat(p); e == nil && st.Mode().IsRegular() && st.Mode().Perm()&0111 != 0 {
				path = p
				err = nil
				break
			}
		}
	}
	unknown := func(message string) secureDNSResult {
		return secureDNSResult{Protocol: "KeeneticOS", Server: "Настройки DNS", Status: "unknown", Error: message}
	}
	if err != nil {
		return []secureDNSResult{unknown("CLI ndmc не найден; список серверов KeeneticOS недоступен")}
	}
	read := func(command string, parse func(string) []secureDNSResult) []secureDNSResult {
		qctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		b, err := exec.CommandContext(qctx, path, "-c", command).Output()
		if err != nil || len(b) > 4<<20 {
			return nil
		}
		return parse(string(b))
	}
	configured := read("show running-config", configuredDNS)
	current := read("show dns-proxy", proxyDNSMembers)
	if len(configured)+len(current) == 0 {
		return []secureDNSResult{unknown("Не удалось определить серверы из show running-config и show dns-proxy")}
	}
	return append(configured, current...)
}

func resolvServers(raw string) []string {
	var out []string
	for _, line := range strings.Split(raw, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 || f[0] != "nameserver" {
			continue
		}
		ip := net.ParseIP(f[1])
		if ip == nil || ip.IsUnspecified() || ip.IsMulticast() {
			continue
		}
		value := ip.String()
		found := false
		for _, v := range out {
			if v == value {
				found = true
				break
			}
		}
		if !found {
			out = append(out, value)
		}
		if len(out) == 4 {
			break
		}
	}
	return out
}

func lookupDNSAt(ctx context.Context, address, protocol string) secureDNSResult {
	r := secureDNSResult{Protocol: protocol, Server: address, Status: "failed"}
	start := time.Now()
	qctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	endpoint := net.JoinHostPort(address, "53")
	if err := queryRouterDNS(qctx, endpoint); err != nil {
		r.Error = err.Error()
		var networkError net.Error
		if qctx.Err() == context.DeadlineExceeded || (errors.As(err, &networkError) && networkError.Timeout()) {
			r.Status = "timeout"
		}
	} else {
		r.Status = "ok"
	}
	r.Milliseconds = time.Since(start).Milliseconds()
	return r
}

func routerDNSTargets(addresses ...string) []string {
	var targets []string
	for _, value := range addresses {
		ip := net.ParseIP(value)
		if ip == nil || (!ip.IsPrivate() && !ip.IsLoopback()) {
			continue
		}
		value = ip.String()
		duplicate := false
		for _, target := range targets {
			if target == value {
				duplicate = true
				break
			}
		}
		if !duplicate {
			targets = append(targets, value)
		}
	}
	return targets
}

func routerDNSProbe(ctx context.Context, panelIP string) []secureDNSResult {
	var out []secureDNSResult
	seen := map[string]bool{}
	try := func(targets []string) bool {
		for _, ip := range targets {
			if seen[ip] || ctx.Err() != nil {
				continue
			}
			seen[ip] = true
			row := lookupDNSAt(ctx, ip, "DNS роутера")
			out = append(out, row)
			if row.Status == "ok" {
				return true
			}
		}
		return false
	}
	// The server-side socket address cannot be overridden by the browser's Host header.
	if !try(routerDNSTargets(panelIP)) {
		try(routerDNSTargets(DetectNetwork().LAN, "127.0.0.1", "::1"))
	}
	if raw, err := os.ReadFile("/etc/resolv.conf"); err == nil {
		for _, ip := range resolvServers(string(raw)) {
			if seen[ip] {
				continue
			}
			out = append(out, secureDNSResult{Protocol: "DNS ОС", Server: ip, Status: "configured", Error: "Указан в /etc/resolv.conf; не обязательно upstream Keenetic"})
		}
	} else {
		out = append(out, secureDNSResult{Protocol: "DNS ОС", Server: "/etc/resolv.conf", Status: "unknown", Error: "Не удалось прочитать список резолверов"})
	}
	return append(out, readRouterDNSConfig(ctx)...)
}
