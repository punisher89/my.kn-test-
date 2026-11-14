package pilot

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const passwordIterations = 150000

type passwordRecord struct {
	Salt       []byte `json:"salt"`
	Hash       []byte `json:"hash"`
	Iterations int    `json:"iterations"`
}
type pairingRecord struct {
	Hash    []byte    `json:"hash"`
	Expires time.Time `json:"expires"`
}
type loginLimit struct {
	Count int
	Since time.Time
}
type Auth struct {
	mu       sync.Mutex
	dir      string
	sessions map[string]time.Time
	limits   map[string]loginLimit
}

func NewAuth(dir string) *Auth {
	return &Auth{dir: dir, sessions: map[string]time.Time{}, limits: map[string]loginLimit{}}
}

func Pair(dir string) (string, error) {
	if _, err := os.Stat(filepath.Join(dir, "password.json")); err == nil {
		return "", errors.New("пароль уже задан; войдите в панель с ним")
	} else if !os.IsNotExist(err) {
		return "", err
	}
	n, err := rand.Int(rand.Reader, big.NewInt(100000000))
	if err != nil {
		return "", err
	}
	code := fmt.Sprintf("%08d", n.Int64())
	hash := sha256.Sum256([]byte(code))
	if err = WriteJSON(filepath.Join(dir, "pairing.json"), pairingRecord{Hash: hash[:], Expires: time.Now().Add(15 * time.Minute)}); err != nil {
		return "", err
	}
	return code, nil
}
func (a *Auth) configured() bool {
	_, err := os.Stat(filepath.Join(a.dir, "password.json"))
	return err == nil
}
func (a *Auth) pairing() pairingRecord {
	var p pairingRecord
	if b, err := os.ReadFile(filepath.Join(a.dir, "pairing.json")); err == nil {
		_ = json.Unmarshal(b, &p)
	}
	return p
}
func (a *Auth) validSession(r *http.Request) bool {
	c, err := r.Cookie("pilot-session")
	if err != nil {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.sessions[c.Value].After(time.Now())
}
func (a *Auth) allow(r *http.Request) bool {
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	now := time.Now()
	for key, lim := range a.limits {
		if now.Sub(lim.Since) > time.Minute {
			delete(a.limits, key)
		}
	}
	if len(a.limits) > 1024 {
		return false
	}
	v := a.limits[ip]
	if now.Sub(v.Since) > time.Minute {
		v = loginLimit{Since: now}
	}
	v.Count++
	a.limits[ip] = v
	return v.Count <= 8
}
func (a *Auth) cookie(w http.ResponseWriter, r *http.Request) error {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return err
	}
	key := hex.EncodeToString(b)
	now := time.Now()
	for k, expiry := range a.sessions {
		if !expiry.After(now) {
			delete(a.sessions, k)
		}
	}
	if len(a.sessions) >= 32 {
		for k := range a.sessions {
			delete(a.sessions, k)
			break
		}
	}
	a.sessions[key] = now.Add(12 * time.Hour)
	http.SetCookie(w, &http.Cookie{Name: "pilot-session", Value: key, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil, MaxAge: 12 * 3600})
	return nil
}
func sameOrigin(r *http.Request) bool {
	// Browser POSTs must originate at this panel; command-line clients without
	// Origin still need the pairing code or password.
	origin := r.Header.Get("Origin")
	return origin == "" || origin == "http://"+r.Host || origin == "https://"+r.Host
}
func (a *Auth) Register(mux *http.ServeMux) {
	reply := func(w http.ResponseWriter, code int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(v)
	}
	mux.HandleFunc("GET /auth/status", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		configured := a.configured()
		available := a.pairing().Expires.After(time.Now())
		a.mu.Unlock()
		reply(w, 200, map[string]bool{"configured": configured, "pairing_available": available && !configured, "authenticated": a.validSession(r)})
	})
	for _, path := range []string{"/auth/setup", "/auth/login"} {
		path := path
		mux.HandleFunc("POST "+path, func(w http.ResponseWriter, r *http.Request) {
			if !sameOrigin(r) {
				reply(w, 403, map[string]string{"error": "чужой Origin запрещён"})
				return
			}
			a.mu.Lock()
			defer a.mu.Unlock()
			if !a.allow(r) {
				reply(w, 429, map[string]string{"error": "слишком много попыток; повторите через минуту"})
				return
			}
			var input struct {
				Code     string `json:"code"`
				Password string `json:"password"`
			}
			decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048))
			decoder.DisallowUnknownFields()
			if decoder.Decode(&input) != nil {
				reply(w, 400, map[string]string{"error": "некорректный запрос"})
				return
			}
			if len(input.Password) > 128 {
				reply(w, 400, map[string]string{"error": "пароль слишком длинный"})
				return
			}
			if path == "/auth/setup" {
				if a.configured() {
					reply(w, 409, map[string]string{"error": "пароль уже задан"})
					return
				}
				p := a.pairing()
				hash := sha256.Sum256([]byte(input.Code))
				if !p.Expires.After(time.Now()) || subtle.ConstantTimeCompare(p.Hash, hash[:]) != 1 {
					reply(w, 401, map[string]string{"error": "код неверен или истёк; обновите его командой pilot --pair"})
					return
				}
				if len(input.Password) < 10 {
					reply(w, 400, map[string]string{"error": "нужно не менее 10 символов пароля"})
					return
				}
				salt := make([]byte, 16)
				if _, err := rand.Read(salt); err != nil {
					reply(w, 500, map[string]string{"error": "не удалось создать пароль"})
					return
				}
				hashPassword, err := pbkdf2.Key(sha256.New, input.Password, salt, passwordIterations, 32)
				if err != nil {
					reply(w, 500, map[string]string{"error": "не удалось создать пароль"})
					return
				}
				if err = WriteJSON(filepath.Join(a.dir, "password.json"), passwordRecord{Salt: salt, Hash: hashPassword, Iterations: passwordIterations}); err != nil {
					reply(w, 500, map[string]string{"error": "не удалось сохранить пароль"})
					return
				}
				_ = os.Remove(filepath.Join(a.dir, "pairing.json"))
			} else {
				var record passwordRecord
				b, err := os.ReadFile(filepath.Join(a.dir, "password.json"))
				if err != nil || json.Unmarshal(b, &record) != nil || record.Iterations != passwordIterations {
					reply(w, 401, map[string]string{"error": "вход недоступен; завершите первичную настройку"})
					return
				}
				hashPassword, err := pbkdf2.Key(sha256.New, input.Password, record.Salt, record.Iterations, 32)
				if err != nil || subtle.ConstantTimeCompare(hashPassword, record.Hash) != 1 {
					reply(w, 401, map[string]string{"error": "неверный пароль"})
					return
				}
			}
			if err := a.cookie(w, r); err != nil {
				reply(w, 500, map[string]string{"error": "не удалось создать сеанс"})
				return
			}
			reply(w, 200, map[string]bool{"ok": true})
		})
	}
	mux.HandleFunc("POST /auth/logout", func(w http.ResponseWriter, r *http.Request) {
		if !sameOrigin(r) {
			w.WriteHeader(403)
			return
		}
		a.mu.Lock()
		if c, err := r.Cookie("pilot-session"); err == nil {
			delete(a.sessions, c.Value)
		}
		a.mu.Unlock()
		http.SetCookie(w, &http.Cookie{Name: "pilot-session", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode})
		reply(w, 200, map[string]bool{"ok": true})
	})
}
