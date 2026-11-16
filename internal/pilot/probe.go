package pilot

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"
)

type ProbeResult struct {
	ClientHello bool   `json:"client_hello_response,omitempty"`
	Peer        string `json:"peer,omitempty"`
	URL         string `json:"url"`
	TLS         string `json:"tls"`
	Family      string `json:"family"`
	Direct      bool   `json:"direct"`
	Attempt     int    `json:"attempt"`
	OK          bool   `json:"ok"`
	Status      int    `json:"status,omitempty"`
	DurationMS  int64  `json:"duration_ms"`
	Error       string `json:"error,omitempty"`
}

type Prober interface {
	Check(context.Context, Profile, Config, bool) []ProbeResult
}

type HTTPProber struct {
	MarkDirect bool
	TrialMark  bool
	Interface  string
	PinnedIP   string
}

func publicIP(ip net.IP) bool {
	return ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() &&
		!net.ParseIP("100.64.0.0").Equal(ip.Mask(net.CIDRMask(10, 32))) &&
		!net.ParseIP("198.18.0.0").Equal(ip.Mask(net.CIDRMask(15, 32)))
}

func (p HTTPProber) Check(ctx context.Context, profile Profile, c Config, direct bool) []ProbeResult {
	results := []ProbeResult{}
	families := []string{"tcp4"}
	if c.IPv6 {
		families = append(families, "tcp6")
	}
	for _, target := range profile.Targets {
		for _, family := range families {
			for _, version := range []uint16{tls.VersionTLS12, tls.VersionTLS13} {
				for attempt := 1; attempt <= c.Attempts; attempt++ {
					if ctx.Err() != nil {
						return results
					}
					r := p.probe(ctx, target, family, version, time.Duration(c.TimeoutSeconds)*time.Second, direct)
					r.Attempt = attempt
					results = append(results, r)
					if c.probeFailFast && !r.OK {
						return results
					}
				}
			}
		}
	}
	return results
}

func (p HTTPProber) probe(parent context.Context, target Target, family string, version uint16, timeout time.Duration, direct bool) (r ProbeResult) {
	r = ProbeResult{URL: target.URL, Family: family, TLS: map[uint16]string{tls.VersionTLS12: "1.2", tls.VersionTLS13: "1.3"}[version], Direct: direct}
	start := time.Now()
	defer func() { r.DurationMS = time.Since(start).Milliseconds() }()
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	u, err := url.Parse(target.URL)
	if err != nil {
		r.Error = err.Error()
		return
	}
	transport := &http.Transport{
		Proxy: nil, DisableKeepAlives: true, ForceAttemptHTTP2: false,
		TLSClientConfig: &tls.Config{MinVersion: version, MaxVersion: version, ServerName: u.Hostname()},
		DialContext: func(ctx context.Context, _, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			if host != u.Hostname() {
				return nil, errors.New("неожиданный хост")
			}
			return p.dialPublic(ctx, host, port, family, timeout, direct)
		},
	}
	defer transport.CloseIdleConnections()
	result, err := performRequest(ctx, transport, target)
	r.Status = result
	if err != nil {
		r.Error = err.Error()
		return
	}
	r.OK = true
	return
}

func (p HTTPProber) dialPublic(ctx context.Context, host, port, family string, timeout time.Duration, direct bool) (net.Conn, error) {
	var ips []net.IP
	var err error
	if p.PinnedIP != "" {
		ip := net.ParseIP(p.PinnedIP)
		if ip == nil {
			return nil, errors.New("неверный закреплённый IP")
		}
		if (family == "tcp4") != (ip.To4() != nil) {
			return nil, errors.New("DNS: семейство IP не совпадает")
		}
		ips = []net.IP{ip}
	} else {
		ips, err = net.DefaultResolver.LookupIP(ctx, "ip"+strings.TrimPrefix(family, "tcp"), host)
	}
	if err != nil {
		return nil, fmt.Errorf("DNS: %w", err)
	}
	if len(ips) == 0 {
		return nil, errors.New("DNS: нет адресов выбранного семейства")
	}
	// Pin the resolved address for the actual socket: a second DNS lookup
	// must not allow DNS rebinding to the router or another LAN service.
	for _, ip := range ips {
		if !publicIP(ip) {
			return nil, errors.New("DNS вернул непубличный адрес; проверка запрещена")
		}
	}
	d := net.Dialer{Timeout: timeout}
	if (direct && p.MarkDirect) || p.TrialMark {
		d.Control = func(_, _ string, raw syscall.RawConn) error {
			var socketErr error
			err := raw.Control(func(fd uintptr) {
				if p.Interface != "" {
					socketErr = syscall.SetsockoptString(int(fd), syscall.SOL_SOCKET, syscall.SO_BINDTODEVICE, p.Interface)
					if socketErr != nil {
						return
					}
				}
				socketErr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_MARK, func() int {
					if p.TrialMark {
						return 0x200000dc
					}
					return 0x40000000
				}())
			})
			if err != nil {
				return err
			}
			return socketErr
		}
	}
	var last error
	for _, ip := range ips {
		conn, err := d.DialContext(ctx, family, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
		last = err
	}
	return nil, last
}

func performRequest(ctx context.Context, transport *http.Transport, target Target) (int, error) {
	client := &http.Client{Transport: transport, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.URL, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", "Keenetic-Pilot/"+Version)
	req.Header.Set("Cache-Control", "no-cache")
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != target.Status {
		return resp.StatusCode, fmt.Errorf("HTTP %d; ожидался %d", resp.StatusCode, target.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return resp.StatusCode, fmt.Errorf("чтение ответа: %w", err)
	}
	if target.Contains != "" && !strings.Contains(string(body), target.Contains) {
		return resp.StatusCode, errors.New("ответ не содержит контрольный фрагмент")
	}
	if len(body) == 0 && target.Status != 204 {
		return resp.StatusCode, errors.New("пустой ответ")
	}
	return resp.StatusCode, nil
}

func allPassed(r []ProbeResult, p Profile, c Config) bool {
	want := len(p.Targets) * 2 * c.Attempts
	if c.IPv6 {
		want *= 2
	}
	if len(r) != want {
		return false
	}
	expected := map[string]bool{}
	families := []string{"tcp4"}
	if c.IPv6 {
		families = append(families, "tcp6")
	}
	key := func(u, t, f string, a int) string { return fmt.Sprintf("%s\x00%s\x00%s\x00%d", u, t, f, a) }
	for _, t := range p.Targets {
		for _, family := range families {
			for _, version := range []string{"1.2", "1.3"} {
				for a := 1; a <= c.Attempts; a++ {
					expected[key(t.URL, version, family, a)] = true
				}
			}
		}
	}
	for _, v := range r {
		k := key(v.URL, v.TLS, v.Family, v.Attempt)
		if !v.OK || !expected[k] {
			return false
		}
		delete(expected, k)
	}
	return len(expected) == 0
}
