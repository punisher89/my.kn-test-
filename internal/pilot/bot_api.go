package pilot

import (
 "crypto/subtle"
 "encoding/hex"
 "encoding/json"
 "io"
 "net"
 "net/http"
 "os"
 "path/filepath"
 "strings"
)

// Separate credentials and endpoints, usable only via an SSH forwarding
// connection to the router's own loopback. Never grant access to /api/config,
// diagnostics, arbitrary commands, or the signed updater.
func botAuthorized(r *http.Request, path string) bool {
 host, _, err := net.SplitHostPort(r.RemoteAddr)
 if err != nil { return false }
 ip := net.ParseIP(host)
 if ip == nil || !ip.IsLoopback() || r.Header.Get("Origin") != "" { return false }
 if !strings.HasPrefix(r.Header.Get("Authorization"),"Bearer ") { return false }
 info, err := os.Lstat(path)
 if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size()>128 { return false }
 raw, err := os.ReadFile(path)
 if err != nil { return false }
 key := strings.TrimSpace(string(raw))
 provided := strings.TrimPrefix(r.Header.Get("Authorization"),"Bearer ")
 if len(key)!=64 || len(provided)!=64 { return false }
 if _,err := hex.DecodeString(key);err!=nil { return false }
 return subtle.ConstantTimeCompare([]byte(key),[]byte(provided))==1
}

func registerBotAPI(mux *http.ServeMux, m *Manager) {
 reply:=func(w http.ResponseWriter, code int, data any){
  w.Header().Set("Content-Type","application/json; charset=utf-8")
  w.Header().Set("Cache-Control","no-store")
  w.WriteHeader(code)
  _=json.NewEncoder(w).Encode(data)
 }
 guard:=func(path string, fn http.HandlerFunc){
  mux.HandleFunc(path,func(w http.ResponseWriter,r *http.Request){
   if !botAuthorized(r,filepath.Join(filepath.Dir(m.path),"bot-control-token")) {
    reply(w,401,map[string]string{"error":"bot access denied"});return
   }
   fn(w,r)
  })
 }
 guard("GET /api/bot/status",func(w http.ResponseWriter,r *http.Request){
  s:=m.Snapshot()
  reply(w,200,map[string]any{"version":s.Version,"running":s.Running,"busy":s.Busy,"domain_queue":s.DomainQueue,"background_search":s.BackgroundSearch})
 })
 guard("POST /api/bot/action",func(w http.ResponseWriter,r *http.Request){
  if r.Header.Get("Content-Type")!="application/json"{
   reply(w,415,map[string]string{"error":"JSON required"});return
  }
  var in struct{Action string `json:"action"`}
  r.Body=http.MaxBytesReader(w,r.Body,1024)
  d:=json.NewDecoder(r.Body);d.DisallowUnknownFields()
  if d.Decode(&in)!=nil||d.Decode(new(any))!=io.EOF{
   reply(w,400,map[string]string{"error":"invalid command"});return
  }
  switch in.Action{
  case "on","off","youtube","tune","cancel":
  default:
   reply(w,400,map[string]string{"error":"unknown command"});return
  }
  if in.Action=="cancel"{
   m.CancelTune()
   reply(w,202,map[string]string{"status":"cancelling"});return
  }
  release,err:=m.reserveManual(r.Context())
  if err!=nil {reply(w,409,map[string]string{"error":"busy"});return}
  defer release()
  status:="accepted"
  switch in.Action {
  case "on","off":
   err=m.SetEnabled(in.Action=="on")
  case "tune":
   err=m.BeginTune("youtube")
  case "youtube":
   _,err=m.Check(r.Context(),"youtube")
   if err==nil {
    report:=m.Snapshot().Checks["youtube"]
    status="check incomplete"
    if report.Complete && !report.Stale {
     status="check failed"
     if report.Passed {status="check passed"}
    }
   }
  }
  if err!=nil {reply(w,409,map[string]string{"error":"command failed or busy"});return}
  reply(w,200,map[string]string{"status":status})
 })
}
