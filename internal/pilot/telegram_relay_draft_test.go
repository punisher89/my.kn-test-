package pilot

import (
  "os"
  "path/filepath"
  "strings"
  "testing"
)

func TestTelegramRelayDraftIsDisabledAndOnlySavesOwnHostname(t *testing.T) {
  m,prefix:=updateFixture(t)
  before,_:=os.ReadFile(filepath.Join(prefix,"etc/keenetic-pilot/config.json"))
  if d:=m.TelegramRelayDraft();d.CanRoute||d.WorkerHost!=""||d.Status!="draft_only" {t.Fatal("sidecar active without code",d)}
  good:="my-telegram.example.workers.dev"
  d,err:=m.SaveTelegramRelayDraft(good);if err!=nil {t.Fatal(err)}
  if d.WorkerHost!=good||d.CanRoute||!d.UsesExternalWorker||d.Status!="draft_only" {t.Fatalf("stored draft incorrectly presented as functional: %+v",d)}
  after,_:=os.ReadFile(filepath.Join(prefix,"etc/keenetic-pilot/config.json"))
  if string(after)!=string(before){t.Fatal("telegram draft changed working engine config")}
  onDisk,_:=os.ReadFile(m.telegramRelayDraftPath())
  if strings.Contains(string(onDisk),"telegram_mtproto")||strings.Contains(string(onDisk),"enabled") {t.Fatal("draft may not enable any network sidecar")}
  d,err=m.SaveTelegramRelayDraft("");if err!=nil || d.WorkerHost!=""||d.CanRoute {t.Fatal("could not disable template",d,err)}
}
func TestTelegramRelayDraftRejectsUnexpectedHosts(t *testing.T) {
  m,_:=updateFixture(t)
  for _,host:=range []string{"https://my.worker.dev","1.1.1.1","localhost","router.local","A.WORKERS.DEV","foo.worker.dev/path",
    "foo.worker.dev:443","foo@worker.dev","foo.worker.dev?token=x","foo.worker.dev\nInjected:x",
    strings.Repeat("a",255)+".dev","127.0.0.1","[::1]"} {
    if d,err:=m.SaveTelegramRelayDraft(host);err==nil {t.Fatalf("accepted unsafe host %q: %+v",host,d)}
  }
  if _,err:=os.Stat(m.telegramRelayDraftPath());!os.IsNotExist(err){t.Fatal("invalid values wrote a draft",err)}
}
