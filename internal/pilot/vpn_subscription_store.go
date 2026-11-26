package pilot

import (
 "context"
 "encoding/json"
 "errors"
 "io"
 "os"
 "path/filepath"
 "time"
)

// One explicitly imported subscription; private file, separate from config
// exports, logs, diagnostics and public API responses.
type savedSubscription struct {
 URL string `json:"url"`
 Data []byte `json:"data"`
 Updated time.Time `json:"updated"`
}
type subscriptionState struct {
 Saved bool `json:"saved"`
 Updated time.Time `json:"updated,omitempty"`
 Preview subscriptionPreview `json:"preview"`
}
func (m *Manager) subscriptionPath() string {
 return filepath.Join(filepath.Dir(m.path),"vpn-subscription.private.json")
}
func readSavedSubscription(path string)(savedSubscription,error){
 var s savedSubscription
 f,e:=os.Open(path);if e!=nil{return s,e};defer f.Close()
 info,e:=f.Stat();if e!=nil||!info.Mode().IsRegular()||info.Mode().Perm()&0077!=0{return s,errors.New("небезопасный файл подписки")}
 data,e:=io.ReadAll(io.LimitReader(f,800001));if e!=nil||len(data)>800000{return s,errors.New("файл подписки недоступен")}
 if json.Unmarshal(data,&s)!=nil||s.URL==""||len(s.URL)>4096||len(s.Data)>maxSubscriptionBytes{return s,errors.New("повреждён файл подписки")}
 return s,nil
}
func (m *Manager) SubscriptionState()(subscriptionState,error){
 s,e:=readSavedSubscription(m.subscriptionPath())
 if os.IsNotExist(e){return subscriptionState{},nil}
 if e!=nil{return subscriptionState{},errors.New("не удалось прочитать сохранённую подписку")}
 p,e:=previewSubscription(s.Data);if e!=nil{return subscriptionState{},e}
 return subscriptionState{Saved:true,Updated:s.Updated,Preview:p},nil
}
func (m *Manager) SaveSubscription(ctx context.Context,raw string,refresh bool)(subscriptionState,error){
 if !m.op.TryLock(){return subscriptionState{},ErrBusy};defer m.op.Unlock()
 if m.updateRequested.Load(){return subscriptionState{},ErrBusy}
 return m.saveSubscriptionLocked(ctx,raw,refresh)
}
func (m *Manager) saveSubscriptionLocked(ctx context.Context,raw string,refresh bool)(subscriptionState,error){
 if refresh {
  old,e:=readSavedSubscription(m.subscriptionPath());if e!=nil{return subscriptionState{},errors.New("нет сохранённой подписки")}
  raw=old.URL
 }
 data,p,e:=fetchSubscription(ctx,raw);if e!=nil{return subscriptionState{},e}
 s:=savedSubscription{URL:raw,Data:data,Updated:time.Now().UTC()}
 if e=WriteJSON(m.subscriptionPath(),s);e!=nil{return subscriptionState{},errors.New("не удалось сохранить подписку")}
 return subscriptionState{Saved:true,Updated:s.Updated,Preview:p},nil
}
func (m *Manager) DeleteSubscription()error{
 if !m.op.TryLock(){return ErrBusy};defer m.op.Unlock()
 e:=os.Remove(m.subscriptionPath())
 if e!=nil&&!os.IsNotExist(e){return errors.New("не удалось удалить подписку")}
 return nil
}

// Called only while Watch owns the operation lock, at most once per day.
// No subscription is created implicitly; a previously imported one is required.
func (m *Manager) refreshSubscriptionIfDue(ctx context.Context,now time.Time) error {
 old,e:=readSavedSubscription(m.subscriptionPath())
 if os.IsNotExist(e){return nil}
 if e!=nil{return errors.New("сохранённая подписка недоступна")}
 if !old.Updated.IsZero()&&now.Sub(old.Updated)<24*time.Hour{return nil}
 _,e=m.saveSubscriptionLocked(ctx,"",true)
 return e
}
