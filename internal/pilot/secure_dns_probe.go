package pilot

import (
	"context"
	"errors"
	"net"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

type secureDNSRequest struct {
	Server   string `json:"server"`
	Address  string `json:"address"`
	Protocol string `json:"protocol"`
	panelIP  string
}
type secureDNSResult struct {
	Protocol     string `json:"protocol"`
	Server       string `json:"server"`
	Status       string `json:"status"`
	Milliseconds int64  `json:"milliseconds"`
	Error        string `json:"error,omitempty"`
}

var dnsProbeLabel = regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$`)

func normalizeDNSProbe(in secureDNSRequest) (secureDNSRequest, error) {
	in.Server = strings.TrimSuffix(strings.TrimSpace(in.Server), ".")
	in.Address = strings.TrimSpace(in.Address)
	if in.Protocol == "" {
		if in.Server == "" && in.Address == "" {
			in.Protocol = "router"
		} else {
			in.Protocol = "all"
		}
	}
	if in.Protocol == "router" || in.Protocol == "system" {
		if in.Server != "" || in.Address != "" {
			return in, errors.New("Для проверки DNS роутера не вводите внешний сервер")
		}
		return in, nil
	}
	if in.Server == "" {
		return in, errors.New("Для ручного теста укажите имя DNS-сервера")
	}
	if len(in.Server) > 253 || net.ParseIP(in.Server) != nil {
		return in, errors.New("Укажите DNS-имя сертификата, IP — отдельно")
	}
	for _, label := range strings.Split(in.Server, ".") {
		if !dnsProbeLabel.MatchString(label) {
			return in, errors.New("Некорректное DNS-имя")
		}
	}
	if in.Address != "" {
		ip := net.ParseIP(in.Address)
		if ip == nil || ip.IsUnspecified() || ip.IsMulticast() {
			return in, errors.New("Некорректный IP DNS-сервера")
		}
		in.Address = ip.String()
	}
	switch in.Protocol {
	case "", "all", "system", "DoH", "DoT", "DoQ":
	default:
		return in, errors.New("Неизвестный протокол DNS")
	}
	return in, nil
}

func secureDNSProbe(ctx context.Context, inputs ...secureDNSRequest) []secureDNSResult {
	in := secureDNSRequest{}
	if len(inputs) > 0 {
		in = inputs[0]
	}
	in, err := normalizeDNSProbe(in)
	if err != nil {
		return []secureDNSResult{{Status: "failed", Error: err.Error()}}
	}
	if in.Protocol == "router" {
		return routerDNSProbe(ctx, in.panelIP)
	}
	out := []secureDNSResult{}
	if in.Protocol == "system" {
		r := secureDNSResult{Protocol: "System", Server: "Системный резолвер ОС", Status: "failed"}
		start := time.Now()
		qctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		ips, e := net.DefaultResolver.LookupIP(qctx, "ip4", "example.com")
		if qctx.Err() != nil {
			r.Status = "timeout"
		} else if e == nil {
			for _, ip := range ips {
				if dnsProbeAnswer(ip.String()) {
					r.Status = "ok"
					break
				}
			}
		}
		if r.Status == "failed" {
			r.Error = "Системный резолвер не вернул пригодный A-ответ"
		}
		r.Milliseconds = time.Since(start).Milliseconds()
		cancel()
		out = append(out, r)
		return out
	}
	path, pathErr := exec.LookPath("kdig")
	endpoint := in.Server
	if in.Address != "" {
		endpoint = in.Address
	}
	for _, p := range []struct{ name, option, port string }{{"DoH", "+https", "443"}, {"DoT", "+tls", "853"}, {"DoQ", "+quic", "853"}} {
		if in.Protocol != "" && in.Protocol != "all" && in.Protocol != p.name {
			continue
		}
		r := secureDNSResult{Protocol: p.name, Server: in.Server, Status: "unavailable"}
		if pathErr != nil {
			r.Error = "kdig не найден в PATH"
		} else {
			start := time.Now()
			qctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			b, e := exec.CommandContext(qctx, path, "@"+endpoint, "-p", p.port, p.option, "+tls-ca", "+tls-host="+in.Server, "+timeout=3", "+retry=0", "+short", "example.com", "A").CombinedOutput()
			r.Milliseconds = time.Since(start).Milliseconds()
			r.Status = "failed"
			if qctx.Err() != nil {
				r.Status = "timeout"
			} else if e == nil && dnsProbeAnswer(string(b)) {
				r.Status = "ok"
			} else {
				// Do not include raw tool output, which may contain local resolver details.
				msg := strings.ToLower(string(b))
				switch {
				case strings.Contains(msg, "unknown option") || strings.Contains(msg, "not supported") || strings.Contains(msg, "not compiled"):
					r.Status = "unsupported"
					r.Error = "Сборка kdig не поддерживает протокол"
				case strings.Contains(msg, "certificate") || strings.Contains(msg, "verification"):
					r.Error = "Проверка TLS-сертификата не пройдена"
				default:
					r.Error = "Нет подтверждённого DNS-ответа"
				}
			}
			cancel()
		}
		out = append(out, r)
	}
	return out
}
func dnsProbeAnswer(s string) bool {
	for _, line := range strings.Split(s, "\n") {
		ip := net.ParseIP(strings.TrimSpace(line))
		if ip != nil && ip.To4() != nil && !ip.IsUnspecified() && !ip.IsLoopback() {
			return true
		}
	}
	return false
}
