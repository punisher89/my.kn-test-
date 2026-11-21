package pilot

import (
  "path/filepath"
 "reflect"
  "testing"
)

func TestBackgroundPauseIsReversibleAndNeverRestartsLiveEngine(t *testing.T){
  c:=DefaultConfig()
  c.Enabled=true
  c.WAN="ppp0"
  c.ProbeLoadGuard=false
  path:=filepath.Join(t.TempDir(),"config.json")
  if err:=WriteJSON(path,c);err!=nil{t.Fatal(err)}
  b:=&testBackend{running:true}
  m:=NewManager(c,path,b,successfulProber{},&LogBuffer{})
  initial:=cloneConfig(m.Config())
  c.BackgroundPaused=true
  if err:=m.Change(c);err!=nil{t.Fatal("cannot pause safely",err)}
  if len(b.configs)!=0||!b.Running(){t.Fatal("pause restarted or stopped the live bypass")}
  if m.backgroundAllowed(m.Config()){t.Fatal("background work continued through pause")}
  if m.Snapshot().BackgroundHold!="фоновые проверки приостановлены пользователем"{t.Fatal("pause not visible in diagnostics")}
  saved,err:=LoadConfig(path);if err!=nil{t.Fatal(err)}
  if !saved.BackgroundPaused{t.Fatal("pause was not persisted")}
  saved.BackgroundPaused=false
  if err:=m.Change(saved);err!=nil{t.Fatal("cannot resume safely",err)}
  if len(b.configs)!=0||!b.Running(){t.Fatal("resume restarted the live bypass")}
  if !m.backgroundAllowed(m.Config()){t.Fatal("background remained paused after resume")}
  after:=m.Config()
  after.BackgroundPaused=initial.BackgroundPaused
  if !reflect.DeepEqual(initial,after){t.Fatal("pause changed unrelated network configuration")}
}
