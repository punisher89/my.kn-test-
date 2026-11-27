package pilot

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"os"
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
	const profile = `{"action":"save","profile":"[Interface]\nPrivateKey = AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=\nAddress = 10.77.66.2/32\n[Peer]\nPublicKey = BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB=\nEndpoint = vpn.example.org:51820\nAllowedIPs = 91.108.56.0/22,91.108.4.0/22,91.108.8.0/22,91.108.16.0/22,91.108.12.0/22,149.154.160.0/20,91.105.192.0/23,91.108.20.0/22,185.76.151.0/24\nPersistentKeepalive = 25\n"}`
	if w := post(profile, false); w.Code != http.StatusForbidden {
		t.Fatalf("HTTP: %d", w.Code)
	}
	if w := post(profile, true); w.Code != http.StatusOK || strings.Contains(w.Body.String(), "PrivateKey") {
		t.Fatalf("HTTPS: %d, want success without returning credentials", w.Code)
	}
	if info, err := os.Stat(m.telegramProfilePath()); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("profile was not stored privately: %v", err)
	}
	if w := post(strings.Replace(profile, "/32", "/0", 1), true); w.Code != http.StatusBadRequest {
		t.Fatalf("invalid replacement: %d", w.Code)
	}
	got, err := os.ReadFile(m.telegramProfilePath())
	if err != nil || !strings.Contains(string(got), "Address = 10.77.66.2/32") {
		t.Fatal("invalid replacement changed saved profile")
	}
}
