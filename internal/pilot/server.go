package pilot

import (
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"strconv"
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
			// Only user-initiated operations that contend for the controller lock
			// get a foreground reservation. Polling and explicit cancellation do not.
			foreground := false
			switch path {
			case "PUT /api/config", "POST /api/service", "POST /api/tune",
				"POST /api/check", "POST /api/domain-search", "POST /api/adaptive/domain",
				"POST /api/services", "POST /api/services/custom", "DELETE /api/services/{id}",
				"POST /api/services/{id}/domains", "POST /api/settings-backup/restore",
				"POST /api/catalog/update", "POST /api/youtube/video-test", "POST /api/vpn/subscription", "POST /api/vpn/subscription/file":
				foreground = true
			}
			if foreground {
				release, err := m.reserveManual(r.Context())
				if err != nil { jsonReply(w, 409, map[string]string{"error": err.Error()}); return }
				defer release()
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
		var retry checkRetryAfter
		if errors.As(err, &retry) {
			w.Header().Set("Retry-After", strconv.Itoa(int(retry)))
			code = http.StatusTooManyRequests
		} else if errors.Is(err, ErrBusy) {
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
	api("GET /api/settings-backup",func(w http.ResponseWriter,r *http.Request){
  backup,err:=m.SettingsBackup();if err!=nil {replyError(w,err);return}
  w.Header().Set("Content-Disposition",`attachment; filename="pilot-settings-backup.json"`)
  jsonReply(w,200,backup)
 })
 api("POST /api/settings-backup/restore",func(w http.ResponseWriter,r *http.Request){
  var b settingsBackup;if !read(w,r,&b){return}
  if err:=m.RestoreSettingsBackup(b);err!=nil{replyError(w,err);return}
  jsonReply(w,200,m.Snapshot())
 })
 api("GET /api/updates", func(w http.ResponseWriter, r *http.Request) { jsonReply(w, 200, m.Updates()) })
	api("POST /api/updates/check", func(w http.ResponseWriter, r *http.Request) {
		if e := m.CheckUpdate(r.Context()); e != nil {
			replyError(w, e)
			return
		}
		jsonReply(w, 200, m.Updates())
	})
	api("POST /api/updates/install", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Version string `json:"version"`
		}
		if !read(w, r, &input) {
			return
		}
		if e := m.StartUpdate(input.Version); e != nil {
			replyError(w, e)
			return
		}
		jsonReply(w, 202, m.Updates())
	})
	api("GET /api/updates/sources", func(w http.ResponseWriter, r *http.Request) {
		jsonReply(w, 200, m.SourceUpdates())
	})
	api("POST /api/updates/sources/check", func(w http.ResponseWriter, r *http.Request) {
		var input struct{ Source string `json:"source"` }
		if !read(w,r,&input) {return}
		if err:=m.CheckSourceUpdate(r.Context(),input.Source);err!=nil {replyError(w,err);return}
		jsonReply(w,200,m.SourceUpdates())
	})
	api("POST /api/updates/sources/install", func(w http.ResponseWriter, r *http.Request) {
		var input struct {Source string `json:"source"`; Version string `json:"version"`}
		if !read(w,r,&input){return}
		if err:=m.StartSourceUpdate(input.Source,input.Version);err!=nil {replyError(w,err);return}
		jsonReply(w,202,m.SourceUpdates())
	})
	api("GET /api/telegram/relay-draft", func(w http.ResponseWriter, r *http.Request) {
		jsonReply(w, 200, m.TelegramRelayDraft())
	})
	api("PUT /api/telegram/relay-draft", func(w http.ResponseWriter, r *http.Request) {
		var input struct {WorkerHost string `json:"worker_host"`}
		if !read(w,r,&input){return}
		result,err:=m.SaveTelegramRelayDraft(input.WorkerHost)
		if err!=nil {replyError(w,err);return}
		jsonReply(w,200,result)
	})
	api("GET /api/preflight", func(w http.ResponseWriter, r *http.Request) { jsonReply(w, 200, m.Preflight()) })
	api("GET /api/device", func(w http.ResponseWriter, r *http.Request) { jsonReply(w, 200, m.Device()) })
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
		jsonReply(w, 200, m.AdaptiveView())
	})
	api("GET /api/adaptive", func(w http.ResponseWriter, r *http.Request) { jsonReply(w, 200, m.AdaptiveView()) })
	api("POST /api/domain-search", func(w http.ResponseWriter, r *http.Request) {
		var scope DomainScope
		if !read(w, r, &scope) {
			return
		}
		if err := m.QueueDomain(scope); err != nil {
			replyError(w, err)
			return
		}
		jsonReply(w, 202, m.Snapshot())
	})
	api("GET /api/vpn/capabilities", func(w http.ResponseWriter, r *http.Request) {
		jsonReply(w, 200, map[string]any{"engines": independentVPNCapabilities(), "managed_tunnels": false})
	})
	api("GET /api/telegram/tunnel", func(w http.ResponseWriter, r *http.Request) {
		jsonReply(w, 200, m.telegramTunnelStatus())
	})
	telegramRouteCheck := make(chan struct{}, 1)
	api("GET /api/telegram/routes", func(w http.ResponseWriter, r *http.Request) {
		select {
		case telegramRouteCheck <- struct{}{}:
			defer func() { <-telegramRouteCheck }()
		default:
			jsonReply(w, http.StatusConflict, map[string]string{"error": "проверка маршрутов уже выполняется"})
			return
		}
		report, err := m.checkTelegramRoutes(r.Context())
		if err != nil {
			replyError(w, err)
			return
		}
		jsonReply(w, http.StatusOK, report)
	})
	api("POST /api/telegram/tunnel", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Action  string `json:"action"`
			Profile string `json:"profile,omitempty"`
		}
		if !read(w, r, &input) {
			return
		}
		if m.backend.Kind() != "router" {
			replyError(w, errors.New("Telegram-туннель доступен в режиме роутера"))
			return
		}
		if input.Action == "save" && r.TLS == nil {
			jsonReply(w, http.StatusForbidden, map[string]string{"error": "WireGuard-профиль сохраняется через HTTPS-панель"})
			return
		}
		err := m.changeTelegramTunnel(r.Context(), input.Action, input.Profile)
		if err != nil {
			replyError(w, err)
			return
		}
		jsonReply(w, 200, m.telegramTunnelStatus())
	})
	vpnProbeSlot := make(chan struct{}, 1)
 api("GET /api/vpn/subscription",func(w http.ResponseWriter,r *http.Request){
  state,err:=m.SubscriptionState();if err!=nil{replyError(w,err);return};jsonReply(w,200,state)
 })
 api("POST /api/vpn/subscription",func(w http.ResponseWriter,r *http.Request){
  var input struct{URL string `json:"url"`;Action string `json:"action"`}
  if !read(w,r,&input){return}
  if input.Action=="delete"{if err:=m.DeleteSubscription();err!=nil{replyError(w,err);return};jsonReply(w,200,subscriptionState{});return}
  if input.Action!="save"&&input.Action!="refresh"{jsonReply(w,400,map[string]string{"error":"неизвестное действие"});return}
  select{case vpnProbeSlot<-struct{}{}:defer func(){<-vpnProbeSlot}();default:replyError(w,ErrBusy);return}
  state,err:=m.SaveSubscription(r.Context(),input.URL,input.Action=="refresh")
  if err!=nil{replyError(w,err);return};jsonReply(w,200,state)
 })
 // A file can contain credentials: accept it over TLS or an explicit local
 // loopback connection (e.g. an SSH port forward), not unencrypted LAN HTTP.
 api("POST /api/vpn/ssh-check",func(w http.ResponseWriter,r *http.Request){
  if !sshSecretTransport(r){jsonReply(w,403,map[string]string{"error":"Для ввода пароля требуется HTTPS или SSH-проброс на localhost"});return}
  var input sshPasswordRequest
  if !read(w,r,&input){return}
  if err:=validateSSHPassword(input);err!=nil{replyError(w,err);return}
  select{case vpnProbeSlot<-struct{}{}:defer func(){<-vpnProbeSlot}();default:replyError(w,ErrBusy);return}
  jsonReply(w,200,checkSSHPassword(r.Context(),m.path,input))
 })
 api("POST /api/vpn/subscription/file",func(w http.ResponseWriter,r *http.Request){
  remote,_,_:=net.SplitHostPort(r.RemoteAddr)
  host:=r.Host
  if h,_,e:=net.SplitHostPort(host);e==nil {host=h}
  loopback:=net.ParseIP(remote).IsLoopback()&&(host=="localhost"||net.ParseIP(strings.Trim(host,"[]")).IsLoopback())
  if r.TLS==nil&&!loopback{jsonReply(w,403,map[string]string{"error":"Для импорта секретов используйте HTTPS или SSH-проброс панели на localhost"});return}
  if r.Header.Get("Content-Type")!="application/octet-stream"{jsonReply(w,415,map[string]string{"error":"ожидается файл"});return}
  data,err:=io.ReadAll(http.MaxBytesReader(w,r.Body,maxSubscriptionBytes))
  if err!=nil{jsonReply(w,400,map[string]string{"error":"файл слишком большой или не читается"});return}
  state,err:=m.ImportSubscriptionFile(data);if err!=nil{replyError(w,err);return};jsonReply(w,200,state)
 })
 api("POST /api/vpn/subscription/preview",func(w http.ResponseWriter,r *http.Request){
  var input struct{URL string `json:"url"`};if !read(w,r,&input){return}
  select{case vpnProbeSlot<-struct{}{}:defer func(){<-vpnProbeSlot}();default:replyError(w,ErrBusy);return}
  preview,err:=fetchSubscriptionPreview(r.Context(),input.URL)
  if err!=nil{replyError(w,err);return};jsonReply(w,200,preview)
 })
	dnsProbeSlot := make(chan struct{}, 1)
	api("POST /api/dns/probe", func(w http.ResponseWriter, r *http.Request) {
		var input secureDNSRequest
		if !read(w,r,&input){return}
		if _,err:=normalizeDNSProbe(input);err!=nil{replyError(w,err);return}
		select {
		case dnsProbeSlot <- struct{}{}:
			defer func(){<-dnsProbeSlot}()
		default:
			replyError(w,ErrBusy);return
		}
		if local, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr); ok {
			input.panelIP, _, _ = net.SplitHostPort(local.String())
		}
		jsonReply(w,200,secureDNSProbe(r.Context(),input))
	})
	api("GET /api/network", func(w http.ResponseWriter, r *http.Request) { jsonReply(w, 200, DetectNetwork()) })
	api("POST /api/services/custom", func(w http.ResponseWriter, r *http.Request) {
		var p Profile
		if !read(w, r, &p) {
			return
		}
		if e := m.AddCustomService(p); e != nil {
			replyError(w, e)
			return
		}
		jsonReply(w, 200, m.Snapshot())
	})
	api("POST /api/services/{id}/domains", func(w http.ResponseWriter, r *http.Request) {
		if e := m.UpdateServiceDomains(r.Context(), r.PathValue("id")); e != nil {
			replyError(w, e)
			return
		}
		jsonReply(w, 200, m.Snapshot())
	})
	api("POST /api/services", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			ID string `json:"id"`
		}
		if !read(w, r, &input) {
			return
		}
		if err := m.AddService(input.ID); err != nil {
			replyError(w, err)
			return
		}
		jsonReply(w, 200, m.Snapshot())
	})
	api("DELETE /api/services/{id}", func(w http.ResponseWriter, r *http.Request) {
		if err := m.RemoveService(r.PathValue("id")); err != nil {
			replyError(w, err)
			return
		}
		jsonReply(w, 200, m.Snapshot())
	})
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
	api("POST /api/youtube/video-test", func(w http.ResponseWriter, r *http.Request) {
		var input struct { URL string `json:"url"` }
		if !read(w, r, &input) { return }
		result, err := m.CheckYouTubeVideo(r.Context(), input.URL)
		if err != nil { replyError(w, err); return }
		jsonReply(w, 200, result)
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
	registerBotAPI(mux, m)
	root, _ := fs.Sub(assets, "web")
	mux.Handle("/", http.FileServer(http.FS(root)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; media-src 'self' blob: https:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		mux.ServeHTTP(w, r)
	})
}
