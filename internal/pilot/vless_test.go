package pilot

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

const exampleVLESS = "vless://5783a3e7-e373-51cd-8642-c83782b807c5@example.com:443?type=tcp&security=tls&sni=example.com&encryption=none#test"

func TestVLESSConfig(t *testing.T) {
	tls, err := vlessConfig(exampleVLESS)
	if err != nil {
		t.Fatal(err)
	}
	wire, err := json.Marshal(tls)
	if err != nil || !strings.Contains(string(wire), `"listen":"127.0.0.1","port":10891`) || strings.Contains(string(wire), "allowInsecure") {
		t.Fatalf("unexpected configuration: %s, %v", wire, err)
	}
	if _, err := vlessConfig(realityTestLink()); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"&allowInsecure=1", "&security=reality", "&extra=value", "&fp=bad%0Avalue"} {
		if _, err := vlessConfig(strings.Replace(exampleVLESS, "#test", suffix, 1)); err == nil {
			t.Fatalf("accepted unsupported option %q", suffix)
		}
	}
	for _, change := range [][2]string{{"type=tcp", "type=ws"}, {"security=tls", "security=none"}, {"example.com:443", "example.com:0"}} {
		if _, err := vlessConfig(strings.Replace(exampleVLESS, change[0], change[1], 1)); err == nil {
			t.Fatalf("accepted unsupported link %q", change[1])
		}
	}
}

func realityTestLink() string {
	link := strings.Replace(exampleVLESS, "security=tls", "security=reality", 1)
	key := make([]byte, 32)
	key[0] = 5
	return strings.Replace(link, "#test", "&pbk="+base64.RawURLEncoding.EncodeToString(key)+"&sid=ab01&flow=xtls-rprx-vision#test", 1)
}

func TestVLESSImportPrivateAndStatusProtected(t *testing.T) {
	dir := t.TempDir()
	if err := SaveVLESSFromSSH(dir, strings.NewReader(exampleVLESS+"\r\n")); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(vlessConfigPath(dir))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("private configuration: %v %v", info, err)
	}
	if err := SaveVLESSFromSSH(dir, strings.NewReader("invalid")); err == nil {
		t.Fatal("accepted invalid import")
	}
	token := strings.Repeat("k", 40)
	m := NewManager(DefaultConfig(), filepath.Join(dir, "config.json"), Development{}, HTTPProber{}, &LogBuffer{})
	handler := Handler(m, token)
	req := httptest.NewRequest("GET", "/api/vpn/vless", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatal("VLESS status not authenticated")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	var status vlessStatus
	if err := json.Unmarshal(w.Body.Bytes(), &status); w.Code != 200 || err != nil || !status.Configured || status.Running ||
		strings.Contains(w.Body.String(), "5783a3e7") {
		t.Fatal("VLESS status exposed profile or reported invalid state")
	}
}

func TestNativeVLESSXrayStartStop(t *testing.T) {
	if os.Getenv("PILOT_TEST_XRAY") == "" {
		t.Skip("set PILOT_TEST_XRAY and put xray in PATH")
	}
	dir := t.TempDir()
	for _, link := range []string{exampleVLESS, realityTestLink()} {
		if err := SaveVLESSFromSSH(dir, strings.NewReader(link)); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		var v vlessProcess
		if err := v.start(ctx, dir); err != nil {
			cancel()
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = v.stop(context.Background(), true) })
		for {
			conn, err := (&net.Dialer{}).DialContext(ctx, "tcp4", vlessSOCKS)
			if err == nil {
				_ = conn.Close()
				break
			}
			if ctx.Err() != nil || !v.status(dir).Running {
				t.Fatal("Xray did not bind SOCKS", err)
			}
			time.Sleep(20 * time.Millisecond)
		}
		v.mu.Lock()
		process := v.cmd.Process
		v.mu.Unlock()
		if err := v.stop(ctx, false); err != nil || v.status(dir).Running || process.Signal(syscall.Signal(0)) == nil {
			t.Fatal("Xray still running after stop", err)
		}
		cancel()
	}
}
