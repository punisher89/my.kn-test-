package pilot

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type telegramTestRouter struct{ Development }

func (telegramTestRouter) Kind() string { return "router" }

func TestTelegramProfileSaveRequiresHTTPSAndKeepsInvalidReplacementOut(t *testing.T) {
	dir := t.TempDir()
	c := DefaultConfig()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	c.EngineDir = filepath.Join(root, "engine")
	m := NewManager(c, filepath.Join(dir, "config.json"), telegramTestRouter{}, HTTPProber{}, &LogBuffer{})
	token := strings.Repeat("x", 32)
	handler := Handler(m, token)
	post := func(body string, secure bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/api/telegram/tunnel", strings.NewReader(body))
		if secure {
			r.TLS = &tls.ConnectionState{}
		}
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	const profile = `{"action":"save","profile":"[Interface]\nPrivateKey = AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=\nAddress = 10.77.66.2/24, fd00::2/64\nDNS = 1.1.1.1\nTable = auto\n[Peer]\nPublicKey = BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB=\nEndpoint = vpn.example.org:51820\nAllowedIPs = 0.0.0.0/0, ::/0\n"}`
	if w := post(profile, false); w.Code != http.StatusForbidden {
		t.Fatalf("HTTP: %d", w.Code)
	}
	if w := post(profile, true); w.Code != http.StatusOK || strings.Contains(w.Body.String(), "PrivateKey") {
		t.Fatalf("HTTPS: %d, want success without returning credentials", w.Code)
	}
	if info, err := os.Stat(m.telegramProfilePath()); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("profile was not stored privately: %v", err)
	}
	if w := post(strings.Replace(profile, "/24", "/0", 1), true); w.Code != http.StatusBadRequest {
		t.Fatalf("invalid replacement: %d", w.Code)
	}
	for _, invalid := range []string{
		strings.Replace(profile, "DNS = 1.1.1.1", "PostUp = secret-hook", 1),
		strings.Replace(profile, "DNS = 1.1.1.1", "Jc = 4", 1),
		strings.Replace(profile, "Table = auto", "[Peer]", 1),
		`{"action":"save","profile":"vless://secret-link"}`,
	} {
		w := post(invalid, true)
		if w.Code != http.StatusBadRequest || strings.Contains(w.Body.String(), "secret-") {
			t.Fatalf("invalid import: status %d or leaked input", w.Code)
		}
	}
	got, err := os.ReadFile(m.telegramProfilePath())
	if err != nil || !strings.Contains(string(got), "Address = 10.77.66.2/32") {
		t.Fatal("invalid replacement changed saved profile")
	}
	for _, item := range []string{"Address = 10.77.66.2/32", "AllowedIPs = " + telegramAllowedIPs, "PersistentKeepalive = 25"} {
		if !strings.Contains(string(got), item) {
			t.Fatalf("normalized profile missing %s", item)
		}
	}
	for _, item := range []string{"DNS", "Table", "0.0.0.0/0", "fd00::"} {
		if strings.Contains(string(got), item) {
			t.Fatalf("normalized profile contains %s", item)
		}
	}
	if err := exec.Command("sh", m.telegramScript(), "validate", m.telegramProfilePath()).Run(); err != nil {
		t.Fatalf("installed shell helper rejects normalized profile: %v", err)
	}
}
