package pilot

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func authCall(h http.Handler, path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
	method := "POST"
	if path == "/api/status" || path == "/auth/status" {
		method = "GET"
	}
	r := httptest.NewRequest(method, "http://router.test"+path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func TestPairPasswordCookieAndRestart(t *testing.T) {
	dir := t.TempDir()
	code, err := Pair(dir)
	if err != nil {
		t.Fatal(err)
	}
	m := NewManager(DefaultConfig(), filepath.Join(dir, "config.json"), Development{}, HTTPProber{}, &LogBuffer{})
	h := Handler(m, strings.Repeat("a", 64), NewAuth(dir))
	if authCall(h, "/api/status", "", nil).Code != 401 {
		t.Fatal("anonymous access")
	}
	if authCall(h, "/auth/setup", `{"code":"bad","password":"test-password-long"}`, nil).Code != 401 {
		t.Fatal("bad pairing code accepted")
	}
	body := fmt.Sprintf(`{"code":%q,"password":"test-password-long"}`, code)
	w := authCall(h, "/auth/setup", body, nil)
	if w.Code != 200 {
		t.Fatalf("setup failed: %d", w.Code)
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatal("unprotected session cookie")
	}
	if authCall(h, "/api/status", "", cookies[0]).Code != 200 {
		t.Fatal("cookie session rejected")
	}
	if authCall(h, "/auth/setup", body, nil).Code != 409 {
		t.Fatal("password overwritten")
	}
	if _, err = Pair(dir); err == nil {
		t.Fatal("pair command replaced configured password")
	}
	b, err := os.ReadFile(filepath.Join(dir, "password.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "test-password-long") {
		t.Fatal("password stored in plaintext")
	}
	if _, err = os.Stat(filepath.Join(dir, "pairing.json")); !os.IsNotExist(err) {
		t.Fatal("pairing code not consumed")
	}
	h = Handler(m, strings.Repeat("a", 64), NewAuth(dir))
	if authCall(h, "/api/status", "", cookies[0]).Code != 401 {
		t.Fatal("old session survived restart")
	}
	if authCall(h, "/auth/login", `{"password":"wrong"}`, nil).Code != 401 {
		t.Fatal("wrong password accepted")
	}
	w = authCall(h, "/auth/login", `{"password":"test-password-long"}`, nil)
	if w.Code != 200 {
		t.Fatal("password not restored")
	}
	cookie := w.Result().Cookies()[0]
	authCall(h, "/auth/logout", `{}`, cookie)
	if authCall(h, "/api/status", "", cookie).Code != 401 {
		t.Fatal("logout did not revoke session")
	}
}

func TestExpiredPairingOriginAndRateLimit(t *testing.T) {
	dir := t.TempDir()
	code, err := Pair(dir)
	if err != nil {
		t.Fatal(err)
	}
	a := NewAuth(dir)
	m := NewManager(DefaultConfig(), filepath.Join(dir, "config.json"), Development{}, HTTPProber{}, &LogBuffer{})
	h := Handler(m, strings.Repeat("b", 64), a)
	p := a.pairing()
	p.Expires = time.Now().Add(-time.Minute)
	if err = WriteJSON(filepath.Join(dir, "pairing.json"), p); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"code":%q,"password":"another-test-password"}`, code)
	if authCall(h, "/auth/setup", body, nil).Code != 401 {
		t.Fatal("expired code accepted")
	}
	r := httptest.NewRequest("POST", "http://router.test/auth/setup", strings.NewReader(body))
	r.Header.Set("Origin", "http://evil.test")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("foreign origin accepted")
	}
	for i := 0; i < 10; i++ {
		w = authCall(h, "/auth/setup", body, nil)
	}
	if w.Code != 429 {
		t.Fatal("login attempts not limited")
	}
	var status map[string]any
	if err = json.Unmarshal(authCall(h, "/auth/status", "", nil).Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if _, ok := status["code"]; ok {
		t.Fatal("pairing code exposed")
	}
}

func TestNetworkDetectionPrefersLANBridgeAndExcludesWAN(t *testing.T) {
	interfaces := []NetworkInterface{{Name: "ppp0", Addresses: []string{"10.1.1.1"}}, {Name: "br1", Addresses: []string{"192.168.2.1"}}, {Name: "br0", Addresses: []string{"192.168.1.1"}}}
	if got := chooseLAN(interfaces, "ppp0"); got != "192.168.1.1" {
		t.Fatalf("wrong LAN: %s", got)
	}
	if chooseLAN(interfaces[:1], "ppp0") != "" {
		t.Fatal("WAN chosen as LAN")
	}
	if routeInterface("1.1.1.1 via 10.0.0.1 dev ppp0 src 10.0.0.2") != "ppp0" {
		t.Fatal("route lookup failed")
	}
	if chooseLAN([]NetworkInterface{{Name: "x", Addresses: []string{"192.168.1.1"}}, {Name: "y", Addresses: []string{"192.168.2.1"}}}, "") != "" {
		t.Fatal("ambiguous LAN guessed")
	}
}

func TestHTTPAndHTTPSUseSeparateSessionCookies(t *testing.T) {
	dir := t.TempDir()
	code, err := Pair(dir)
	if err != nil {
		t.Fatal(err)
	}
	m := NewManager(DefaultConfig(), filepath.Join(dir, "config.json"), Development{}, HTTPProber{}, &LogBuffer{})
	h := Handler(m, strings.Repeat("a", 64), NewAuth(dir))
	call := func(origin, path, body string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
		method := http.MethodPost
		if path == "/api/status" {
			method = http.MethodGet
		}
		r := httptest.NewRequest(method, origin+path, strings.NewReader(body))
		for _, cookie := range cookies {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	const secure = "https://router.test:9443"
	const insecure = "http://router.test:9080"
	tlsLogin := call(secure, "/auth/setup", fmt.Sprintf(`{"code":%q,"password":"test-password-long"}`, code))
	if tlsLogin.Code != 200 {
		t.Fatalf("HTTPS login: %d", tlsLogin.Code)
	}
	tlsCookie := tlsLogin.Result().Cookies()[0]
	httpLogin := call(insecure, "/auth/login", `{"password":"test-password-long"}`)
	if httpLogin.Code != 200 {
		t.Fatalf("HTTP login: %d", httpLogin.Code)
	}
	httpCookie := httpLogin.Result().Cookies()[0]
	if tlsCookie.Name == httpCookie.Name || !tlsCookie.Secure || httpCookie.Secure {
		t.Fatal("HTTP and HTTPS cookies conflict")
	}
	if call(insecure, "/api/status", "", httpCookie).Code != 200 ||
		call(secure, "/api/status", "", httpCookie, tlsCookie).Code != 200 {
		t.Fatal("one of the parallel sessions was lost")
	}
	if call(secure, "/api/status", "", httpCookie).Code != 401 {
		t.Fatal("HTTP cookie authorized HTTPS")
	}
	call(insecure, "/auth/logout", "", httpCookie)
	if call(insecure, "/api/status", "", httpCookie).Code != 401 ||
		call(secure, "/api/status", "", tlsCookie).Code != 200 {
		t.Fatal("HTTP logout did not stay scoped to HTTP")
	}
}
