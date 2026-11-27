package pilot

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/net/proxy"
)

const vlessSOCKS = "127.0.0.1:10891"

var vlessUUID = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func vlessConfig(raw string) (map[string]any, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || len(raw) > 8192 || u.Scheme != "vless" || u.User == nil || !vlessUUID.MatchString(u.User.Username()) || (u.Path != "" && u.Path != "/") {
		return nil, errors.New("нужна одна ссылка vless:// с UUID клиента")
	}
	if _, hasPassword := u.User.Password(); hasPassword {
		return nil, errors.New("неожиданный пароль в адресе VLESS")
	}
	host := u.Hostname()
	port, err := strconv.Atoi(u.Port())
	if err != nil || port < 1 || port > 65535 || (net.ParseIP(host) == nil && !ValidDomain(strings.ToLower(host))) {
		return nil, errors.New("некорректный адрес или порт VLESS")
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return nil, errors.New("некорректные параметры VLESS")
	}
	for key, values := range q {
		if len(values) != 1 {
			return nil, errors.New("повторяющиеся параметры VLESS")
		}
		switch key {
		case "type", "security", "encryption", "flow", "sni", "fp", "pbk", "sid", "spx", "headerType", "alpn", "allowInsecure", "pqv":
		default:
			return nil, errors.New("ссылка содержит параметр VLESS, который эта версия Pilot ещё не поддерживает")
		}
	}
	transport := q.Get("type")
	if transport != "" && transport != "tcp" && transport != "raw" {
		return nil, errors.New("поддерживается VLESS TCP/RAW")
	}
	if header := q.Get("headerType"); header != "" && header != "none" {
		return nil, errors.New("поддерживается стандартный заголовок TCP VLESS")
	}
	security := q.Get("security")
	if security != "tls" && security != "reality" {
		return nil, errors.New("поддерживается VLESS с TLS или REALITY")
	}
	if enc := q.Get("encryption"); enc != "" && enc != "none" {
		return nil, errors.New("расширенное шифрование VLESS пока не поддерживается")
	}
	if flag := q.Get("allowInsecure"); flag != "" && flag != "0" && flag != "false" {
		return nil, errors.New("проверка TLS-сертификата обязательна")
	}
	flow := q.Get("flow")
	if flow != "" && flow != "xtls-rprx-vision" {
		return nil, errors.New("режим flow не поддерживается")
	}
	sni := q.Get("sni")
	if sni == "" && security == "tls" {
		sni = host
	}
	if net.ParseIP(sni) == nil && !ValidDomain(strings.ToLower(sni)) {
		return nil, errors.New("проверьте имя TLS-сервера (sni)")
	}
	fp := q.Get("fp")
	if fp != "" && !regexp.MustCompile(`^[a-zA-Z0-9_]{1,40}$`).MatchString(fp) {
		return nil, errors.New("некорректный TLS fingerprint")
	}
	stream := map[string]any{"network": "tcp", "security": security}
	switch security {
	case "reality":
		key, err := base64.RawURLEncoding.DecodeString(q.Get("pbk"))
		if err != nil || len(key) != 32 {
			return nil, errors.New("некорректный публичный ключ REALITY (pbk)")
		}
		sid := q.Get("sid")
		if _, err := hex.DecodeString(sid); err != nil || len(sid) > 16 {
			return nil, errors.New("некорректный short ID REALITY")
		}
		if fp == "" {
			fp = "chrome"
		}
		reality := map[string]any{"serverName": sni, "fingerprint": fp, "publicKey": q.Get("pbk"), "shortId": sid, "spiderX": q.Get("spx")}
		if pqv := q.Get("pqv"); pqv != "" {
			reality["mldsa65Verify"] = pqv
		}
		stream["realitySettings"] = reality
	case "tls":
		tls := map[string]any{"serverName": sni}
		if fp != "" {
			tls["fingerprint"] = fp
		}
		if alpn := q.Get("alpn"); alpn != "" {
			tls["alpn"] = strings.Split(alpn, ",")
		}
		stream["tlsSettings"] = tls
	}
	user := map[string]any{"id": u.User.Username(), "encryption": "none", "flow": flow}
	return map[string]any{
		"log": map[string]any{"loglevel": "none"},
		"inbounds": []any{map[string]any{
			"listen": "127.0.0.1", "port": 10891, "protocol": "socks",
			"settings": map[string]any{"auth": "noauth", "udp": false},
		}},
		"outbounds": []any{map[string]any{
			"protocol":       "vless",
			"settings":       map[string]any{"vnext": []any{map[string]any{"address": host, "port": port, "users": []any{user}}}},
			"streamSettings": stream,
		}},
	}, nil
}

func vlessConfigPath(dir string) string {
	return filepath.Join(dir, "vless-xray.private.json")
}

// SaveVLESSFromSSH stores only the generated Xray config, using Pilot's private
// atomic file writes. Import while running takes effect on the next start.
func SaveVLESSFromSSH(dir string, input io.Reader) error {
	b, err := io.ReadAll(io.LimitReader(input, 8193))
	if err != nil {
		return errors.New("не удалось прочитать ссылку VLESS")
	}
	cfg, err := vlessConfig(string(b))
	if err != nil {
		return err
	}
	return WriteJSON(vlessConfigPath(dir), cfg)
}

type vlessStatus struct {
	Available    bool   `json:"available"`
	Configured   bool   `json:"configured"`
	Running      bool   `json:"running"`
	Verified     bool   `json:"verified"`
	SOCKS        string `json:"socks"`
	Milliseconds int64  `json:"milliseconds"`
	Routing      bool   `json:"routing"`
}

type vlessProcess struct {
	mu          sync.Mutex
	cmd         *exec.Cmd
	done        chan struct{}
	closed      bool
	verified    bool
	elapsed     int64
	activeRules *vlessRules
	routeDir    string
}

func (v *vlessProcess) status(dir string) vlessStatus {
	v.mu.Lock()
	defer v.mu.Unlock()
	_, binary := exec.LookPath("xray")
	_, config := os.Stat(vlessConfigPath(dir))
	return vlessStatus{Available: binary == nil, Configured: config == nil, Running: v.cmd != nil, Verified: v.verified, SOCKS: vlessSOCKS, Milliseconds: v.elapsed, Routing: v.cmd != nil && v.activeRules != nil}
}

func (v *vlessProcess) start(ctx context.Context, dir string) error {
	return v.startWithRoutes(ctx, dir, nil, nil)
}

func (v *vlessProcess) startWithRoutes(ctx context.Context, dir string, routing *vlessRules, domains []string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.closed {
		return errors.New("Pilot останавливается")
	}
	if v.cmd != nil {
		if routing != nil {
			return errors.New("сначала отключите текущее подключение VLESS")
		}
		return nil
	}
	path, err := exec.LookPath("xray")
	if err != nil {
		return errors.New("установите Xray в Entware")
	}
	cfg := vlessConfigPath(dir)
	if info, err := os.Lstat(cfg); err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		return errors.New("сначала импортируйте ссылку VLESS через SSH")
	}
	cleanupRuntime := false
	defer func() {
		if cleanupRuntime {
			_ = os.Remove(cfg)
		}
	}()
	if routing != nil {
		if err := cleanupVLESSRoutes(dir); err != nil {
			return err
		}
		raw, err := os.ReadFile(cfg)
		if err != nil {
			return err
		}
		var generated map[string]any
		if err := json.Unmarshal(raw, &generated); err != nil {
			return errors.New("повреждён профиль VLESS")
		}
		if err := addVLESSRouting(generated, routing.LAN, domains); err != nil {
			return err
		}
		cfg = filepath.Join(dir, "vless-runtime.private.json")
		if err := WriteJSON(cfg, generated); err != nil {
			return err
		}
		cleanupRuntime = true
		routeListener, err := net.Listen("tcp4", net.JoinHostPort(routing.LAN, vlessRoutePort))
		if err != nil {
			return errors.New("порт VLESS-маршрутизации 10892 занят")
		}
		_ = routeListener.Close()
	}
	listener, err := net.Listen("tcp4", vlessSOCKS)
	if err != nil {
		return errors.New("локальный порт SOCKS 10891 занят")
	}
	listener.Close()
	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := exec.CommandContext(checkCtx, path, "run", "-test", "-config", cfg).Run(); err != nil {
		return errors.New("Xray отклонил конфигурацию. Проверьте версию Xray и параметры ссылки")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if routing != nil {
		if err := routing.prepare(dir); err != nil {
			return err
		}
		v.activeRules, v.routeDir = routing, dir
	}
	cmd := exec.Command(path, "run", "-config", cfg)
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	if err := cmd.Start(); err != nil {
		if routing != nil && routing.remove(dir) == nil {
			v.activeRules = nil
		}
		return errors.New("не удалось запустить Xray")
	}
	cleanupRuntime = false
	done := make(chan struct{})
	v.cmd, v.done, v.verified, v.elapsed = cmd, done, false, 0
	v.routeDir = dir
	go func() {
		_ = cmd.Wait()
		v.mu.Lock()
		if v.activeRules != nil {
			if v.activeRules.remove(dir) == nil {
				v.activeRules = nil
			}
		}
		if routing != nil {
			_ = os.Remove(cfg)
		}
		v.cmd, v.done, v.verified = nil, nil, false
		close(done)
		v.mu.Unlock()
	}()
	if routing != nil {
		if err := waitVLESSListen(ctx, net.JoinHostPort(routing.LAN, vlessRoutePort)); err != nil {
			_ = cmd.Process.Kill()
			return err
		}
		if err := probeVLESS(ctx); err != nil {
			_ = cmd.Process.Kill()
			return err
		}
		_, _, jumps := routing.rules()
		if err := EnsureRules(RunCommand, jumps); err != nil {
			_ = cmd.Process.Kill()
			return err
		}
		v.verified = true
	}
	return nil
}

func waitVLESSListen(ctx context.Context, address string) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		conn, err := (&net.Dialer{}).DialContext(ctx, "tcp4", address)
		if err == nil {
			return conn.Close()
		}
		select {
		case <-ctx.Done():
			return errors.New("Xray не открыл порт маршрутизации")
		case <-ticker.C:
		}
	}
}

func (v *vlessProcess) stop(ctx context.Context, closeManager bool) error {
	v.mu.Lock()
	v.closed = v.closed || closeManager
	cmd, done := v.cmd, v.done
	if v.activeRules != nil {
		_, _, jumps := v.activeRules.rules()
		if err := RemoveRules(RunCommand, jumps); err != nil {
			v.mu.Unlock()
			return err
		}
	}
	if cmd != nil {
		_ = cmd.Process.Kill()
	}
	v.mu.Unlock()
	if done != nil {
		select {
		case <-done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.activeRules != nil {
		if err := v.activeRules.remove(v.routeDir); err != nil {
			return err
		}
		v.activeRules = nil
	}
	return nil
}

func (v *vlessProcess) check(ctx context.Context) error {
	v.mu.Lock()
	cmd := v.cmd
	v.verified, v.elapsed = false, 0
	v.mu.Unlock()
	if cmd == nil {
		return errors.New("сначала запустите VLESS")
	}
	start := time.Now()
	err := probeVLESS(ctx)
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.cmd != cmd {
		return errors.New("Xray остановился или был перезапущен во время проверки")
	}
	v.verified, v.elapsed = err == nil, time.Since(start).Milliseconds()
	return err
}

func probeVLESS(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	dialer, err := proxy.SOCKS5("tcp4", vlessSOCKS, nil, &net.Dialer{})
	if err != nil {
		return errors.New("не удалось подготовить подключение SOCKS")
	}
	socks, ok := dialer.(proxy.ContextDialer)
	if !ok {
		return errors.New("SOCKS-клиент не поддерживает ограничение времени")
	}
	transport := &http.Transport{DialContext: socks.DialContext, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	request, err := http.NewRequestWithContext(ctx, "GET", "https://example.com/", nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return errors.New("нет HTTPS-ответа через VLESS. Проверьте сервер, транспорт и ключ клиента")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("через VLESS получен HTTP %d вместо 200", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 65536))
	if err != nil || !strings.Contains(string(body), "Example Domain") {
		return errors.New("не получен контрольный ответ через VLESS")
	}
	return nil
}
