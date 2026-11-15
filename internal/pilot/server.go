package pilot

import (
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"strings"
)

//go:embed web/*
var assets embed.FS

func Handler(m *Manager, token string, authOptions ...*Auth) http.Handler {
	mux := http.NewServeMux()
	var auth *Auth
	if len(authOptions) > 0 {
		auth = authOptions[0]
		auth.Register(mux)
	}
	expected := sha256.Sum256([]byte(token))
	jsonReply := func(w http.ResponseWriter, code int, value any) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(value)
	}
	api := func(path string, fn func(http.ResponseWriter, *http.Request)) {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			if origin := r.Header.Get("Origin"); origin != "" {
				u, err := url.Parse(origin)
				if err != nil || u.Host != r.Host || (u.Scheme != "http" && u.Scheme != "https") {
					jsonReply(w, 403, map[string]string{"error": "чужой Origin запрещён"})
					return
				}
			}
			value := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			actual := sha256.Sum256([]byte(value))
			bearerOK := strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") && len(token) >= 32 && subtle.ConstantTimeCompare(expected[:], actual[:]) == 1
			if !bearerOK && (auth == nil || !auth.validSession(r)) {
				jsonReply(w, 401, map[string]string{"error": "требуется ключ доступа"})
				return
			}
			fn(w, r)
		})
	}
	read := func(w http.ResponseWriter, r *http.Request, v any) bool {
		if r.Header.Get("Content-Type") != "application/json" {
			jsonReply(w, 415, map[string]string{"error": "требуется application/json"})
			return false
		}
		r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
		d := json.NewDecoder(r.Body)
		d.DisallowUnknownFields()
		if err := d.Decode(v); err != nil {
			jsonReply(w, 400, map[string]string{"error": err.Error()})
			return false
		}
		if err := d.Decode(new(any)); err != io.EOF {
			jsonReply(w, 400, map[string]string{"error": "ожидался один JSON-объект"})
			return false
		}
		return true
	}
	replyError := func(w http.ResponseWriter, err error) {
		code := 400
		if errors.Is(err, ErrBusy) {
			code = 409
		}
		jsonReply(w, code, map[string]string{"error": err.Error()})
	}
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		jsonReply(w, 200, map[string]string{"status": "ok", "version": Version, "mode": m.backend.Kind()})
	})
	api("GET /api/diagnostics", func(w http.ResponseWriter, r *http.Request) {
		report := m.Diagnostics()
		w.Header().Set("Content-Disposition", `attachment; filename="keenetic-pilot-diagnostics-`+report.GeneratedAt.Format("20060102T150405Z")+`.json"`)
		jsonReply(w, 200, report)
	})
	api("GET /api/status", func(w http.ResponseWriter, r *http.Request) { jsonReply(w, 200, m.Snapshot()) })
	api("POST /api/catalog/update", func(w http.ResponseWriter, r *http.Request) {
		if err := m.UpdateCatalog(r.Context()); err != nil {
			replyError(w, err)
			return
		}
		jsonReply(w, 200, m.Snapshot())
	})
	api("GET /api/logs", func(w http.ResponseWriter, r *http.Request) { jsonReply(w, 200, m.log.Lines()) })
	api("POST /api/adaptive/domain", func(w http.ResponseWriter, r *http.Request) {
		var a DomainAction
		if !read(w, r, &a) {
			return
		}
		if err := m.ControlDomain(a); err != nil {
			replyError(w, err)
			return
		}
		jsonReply(w, 200, ReadAdaptive(m.Config()))
	})
	api("GET /api/adaptive", func(w http.ResponseWriter, r *http.Request) { jsonReply(w, 200, ReadAdaptive(m.Config())) })
	api("GET /api/network", func(w http.ResponseWriter, r *http.Request) { jsonReply(w, 200, DetectNetwork()) })
	api("PUT /api/config", func(w http.ResponseWriter, r *http.Request) {
		var c Config
		if !read(w, r, &c) {
			return
		}
		if err := m.Change(c); err != nil {
			replyError(w, err)
			return
		}
		jsonReply(w, 200, m.Snapshot())
	})
	api("POST /api/service", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Enabled *bool `json:"enabled"`
		}
		if !read(w, r, &input) {
			return
		}
		if input.Enabled == nil {
			replyError(w, errors.New("поле enabled обязательно"))
			return
		}
		if err := m.SetEnabled(*input.Enabled); err != nil {
			replyError(w, err)
			return
		}
		jsonReply(w, 200, m.Snapshot())
	})
	api("POST /api/tune", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Profile string `json:"profile"`
		}
		if !read(w, r, &input) {
			return
		}
		if err := m.BeginTune(input.Profile); err != nil {
			replyError(w, err)
			return
		}
		jsonReply(w, 202, m.Snapshot())
	})
	api("POST /api/tune/cancel", func(w http.ResponseWriter, r *http.Request) {
		m.CancelTune()
		jsonReply(w, 202, map[string]string{"status": "cancelling"})
	})
	api("POST /api/check", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Profile string `json:"profile"`
		}
		if !read(w, r, &input) {
			return
		}
		results, err := m.Check(r.Context(), input.Profile)
		if err != nil {
			replyError(w, err)
			return
		}
		jsonReply(w, 200, results)
	})
	root, _ := fs.Sub(assets, "web")
	mux.Handle("/", http.FileServer(http.FS(root)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		mux.ServeHTTP(w, r)
	})
}
