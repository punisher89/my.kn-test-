package pilot

import (
  "encoding/json"
  "errors"
  "fmt"
  "net"
  "os"
  "path/filepath"
  "strings"
)

// TelegramRelayDraft is a user-controlled integration template for a future
// optional MTProto/WSS sidecar. It is NOT a live proxy configuration.
// Its existence has no effect on nftables, iptables, nfqws2, DNS or routing.
type TelegramRelayDraft struct {
  WorkerHost string `json:"worker_host"`
  Status string `json:"status"`
  CanRoute bool `json:"can_route"`
  Source string `json:"source"`
  UsesExternalWorker bool `json:"uses_external_worker"`
  Notes []string `json:"notes"`
}
type telegramRelayFile struct {
  WorkerHost string `json:"worker_host"`
}
func validWorkerHost(host string) bool {
  if host=="" {return true}
  if len(host)>253 || host!=strings.ToLower(host) || strings.ContainsAny(host,"/:@?#\\ 	\n\r\x00") ||
      net.ParseIP(host)!=nil || !ValidDomain(host) || strings.HasSuffix(host,".local") ||
      strings.HasSuffix(host,".localhost") || strings.HasSuffix(host,".internal") {
    return false
  }
  // A public DNS host only; never accept raw IP, localhost, URL, or userinfo.
  return strings.Contains(host,".") && !strings.HasPrefix(host,".") && host!="localhost"
}
func (m *Manager) telegramRelayDraftPath() string {
  return filepath.Join(filepath.Dir(m.path),"telegram-relay-draft.json")
}
func (m *Manager) TelegramRelayDraft() TelegramRelayDraft {
  doc:=TelegramRelayDraft{Status:"draft_only",CanRoute:false,Source:"t0fox/d2o design reference",UsesExternalWorker:false,
    Notes:[]string{
      "Пока это только заготовка: нет запущенного Telegram sidecar, переподключения TCP и обращений к Worker",
      "Адреса DC d2o получает из встроенного bootstrap и проверенных ответов Telegram help.getConfig; сторонний Worker нужен только для WSS-ретрансляции",
      "Домен Cloudflare Worker задаётся вами; никаких публичных прокси автора автоматически не используется",
      "Существующий nfqws2 MTProto TCP не меняется, YouTube и старые обновления не затрагиваются",
    }}
  b,err:=os.ReadFile(m.telegramRelayDraftPath())
  if err!=nil{return doc}
  var saved telegramRelayFile
  if json.Unmarshal(b,&saved)==nil && validWorkerHost(saved.WorkerHost) {
    doc.WorkerHost=saved.WorkerHost
    doc.UsesExternalWorker=doc.WorkerHost!=""
  }
  return doc
}
func (m *Manager) SaveTelegramRelayDraft(host string) (TelegramRelayDraft,error) {
  host=strings.TrimSpace(host)
  if !validWorkerHost(host) {return TelegramRelayDraft{},errors.New("Нужен публичный hostname своего Worker без https://, порта или пути")}
  if m.updating.Load() || m.updateRequested.Load() {return TelegramRelayDraft{},ErrBusy}
  // No username, password, token or secret may be entered into this field.
  if err:=WriteJSON(m.telegramRelayDraftPath(),telegramRelayFile{WorkerHost:host});err!=nil {
    return TelegramRelayDraft{},fmt.Errorf("не удалось сохранить шаблон Telegram Worker: %w",err)
  }
  return m.TelegramRelayDraft(),nil
}
