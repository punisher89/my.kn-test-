package pilot

import (
 "net/http/httptest"
 "os"
 "path/filepath"
 "strings"
 "testing"
)

func TestBotScope(t *testing.T){
 dir:=t.TempDir()
 key:=strings.Repeat("a",64)
 m:=NewManager(DefaultConfig(),filepath.Join(dir,"config.json"),&testBackend{},successfulProber{},&LogBuffer{})
 h:=Handler(m,strings.Repeat("b",64))
 call:=func(method,path,token,remote,body string)*httptest.ResponseRecorder{
  r:=httptest.NewRequest(method,path,strings.NewReader(body))
  r.RemoteAddr=remote
  r.Header.Set("Authorization","Bearer "+token)
  r.Header.Set("Content-Type","application/json")
  w:=httptest.NewRecorder()
  h.ServeHTTP(w,r)
  return w
 }
 path:=filepath.Join(dir,"bot-control-token")
 if call("GET","/api/bot/status",key,"127.0.0.1:99","").Code!=401 {t.Fatal("enabled by default")}
 if err:=os.WriteFile(path,[]byte(key),0600);err!=nil{t.Fatal(err)}
 w:=call("GET","/api/bot/status",key,"127.0.0.1:99","")
 if w.Code!=200||strings.Contains(w.Body.String(),"config"){t.Fatal("scope status",w.Code,w.Body.String())}
 if call("GET","/api/status",key,"127.0.0.1:99","").Code!=401{t.Fatal("bot elevated to admin")}
 if call("GET","/api/bot/status",key,"192.168.1.2:99","").Code!=401{t.Fatal("LAN token accepted")}
 if call("POST","/api/bot/action",key,"127.0.0.1:99",`{"action":"shell"}`).Code!=400{t.Fatal("arbitrary action accepted")}
 if call("POST","/api/bot/action",key,"127.0.0.1:99",`{"action":"cancel"}`).Code!=202{t.Fatal("cancel rejected")}
 if err:=os.Chmod(path,0644);err!=nil{t.Fatal(err)}
 if call("GET","/api/bot/status",key,"127.0.0.1:99","").Code!=401{t.Fatal("insecure file accepted")}
}
