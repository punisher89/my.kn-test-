package pilot

import (
 "bytes"
 "context"
 "crypto/sha256"
 "encoding/hex"
 "encoding/json"
 "errors"
 "io"
 "net/http"
 "net/http/httptest"
 "os"
 "path/filepath"
 "runtime"
 "strings"
 "testing"
 "time"
)

func fakeSourceRelease() sourceReleaseDocument {
 return sourceReleaseDocument{
    Kind:"pilot-ssh-release",Channel:"ssh",Version:"0.2.0-alpha.20",Source:"ChatGPT",
    GitSHA: strings.Repeat("a",40),PublishedAt:time.Now().UTC(),
    ManifestSHA256:strings.Repeat("b",64),InstallerSHA256:strings.Repeat("c",64),
    EngineVersion:"v1-test",EngineSHA256:strings.Repeat("d",64),InstalledVia:"ssh",
 }
}
func TestSourceReleaseValidationKeepsOriginalSignedTrust(t *testing.T) {
 d:=fakeSourceRelease()
 if err:=validSourceRelease(d,"ChatGPT");err!=nil {t.Fatal(err)}
 for _,change:=range []func(*sourceReleaseDocument){
   func(d *sourceReleaseDocument){d.Source="Codex"},
   func(d *sourceReleaseDocument){d.Kind="pilot-release"},
   func(d *sourceReleaseDocument){d.Channel="packages"},
   func(d *sourceReleaseDocument){d.SignedPanelUpdaterModified=true},
   func(d *sourceReleaseDocument){d.ManifestSHA256=strings.Repeat("0",63)},
   func(d *sourceReleaseDocument){d.EngineSHA256="xyz"},
   func(d *sourceReleaseDocument){d.GitSHA="../private"},
   func(d *sourceReleaseDocument){d.Version="../evil"},
   func(d *sourceReleaseDocument){d.PublishedAt=time.Now().Add(48*time.Hour)},
 } {
   bad:=d;change(&bad)
   if err:=validSourceRelease(bad,"ChatGPT");err==nil {t.Fatalf("untrusted metadata accepted: %+v",bad)}
 }
 if validUpdateSource("Private") {t.Fatal("untrusted publisher accepted")}
}
func TestSourceChecksDoNotReplaceSignedRelease(t *testing.T) {
 oldVersion,oldTransport:=Version,http.DefaultTransport
 defer func(){Version, http.DefaultTransport = oldVersion,oldTransport}()
 Version="0.2.0-alpha.19"
 m,_:=updateFixture(t)
 doc:=fakeSourceRelease()
 original,_:=json.Marshal(doc)
 http.DefaultTransport=updateTransport(func(r *http.Request)(*http.Response,error){
   path:=r.URL.Path
   if !strings.Contains(path,"/ssh-releases/") {t.Fatal("signed package channel was used",path)}
   var data []byte
   if strings.HasSuffix(path,"/latest-chatgpt.json") || strings.HasSuffix(path,"/v0.2.0-alpha.20/release-info.json") {data=original}
   return &http.Response{StatusCode:200,Body:io.NopCloser(bytes.NewReader(data)),Header:make(http.Header)},nil
 })
 if err:=m.CheckSourceUpdate(context.Background(),"ChatGPT");err!=nil {t.Fatal(err)}
 channels:=m.SourceUpdates()
 if !channels.Supported || len(channels.Sources)!=2 || !channels.Sources[0].Available || channels.Sources[0].Source!="ChatGPT" {t.Fatalf("%+v",channels)}
 if channels.Sources[1].Checked {t.Fatal("Codex release accidentally inferred")}
 if m.release!=nil {t.Fatal("existing signed release was overwritten")}
 if err:=m.CheckSourceUpdate(context.Background(),"Other");err==nil {t.Fatal("unknown author accepted")}
 if err:=m.StartSourceUpdate("Codex",doc.Version);err==nil {t.Fatal("unchecked Codex update accepted")}
}
func TestSourceReleaseTamperedPointerAndArchive(t *testing.T) {
 oldVersion,oldTransport:=Version,http.DefaultTransport
 defer func(){Version,http.DefaultTransport=oldVersion,oldTransport}()
 Version="0.2.0-alpha.19"
 m,_:=updateFixture(t)
 doc:=fakeSourceRelease()
 original,_:=json.Marshal(doc)
 http.DefaultTransport=updateTransport(func(r *http.Request)(*http.Response,error){
   data:=append([]byte{},original...)
   if strings.Contains(r.URL.Path,"/release-info.json"){data=append(data,[]byte("tampered")...)}
   return &http.Response{StatusCode:200,Body:io.NopCloser(bytes.NewReader(data)),Header:make(http.Header)},nil
 })
 if err:=m.CheckSourceUpdate(context.Background(),"ChatGPT");err==nil {t.Fatal("mismatched release pointer accepted")}
 if m.SourceUpdates().Sources[0].Available {t.Fatal("untrusted pointer offered as installable")}
 if _,err:=os.Stat(filepath.Join(filepath.Dir(m.path),"update-status.json"));!errors.Is(err,os.ErrNotExist){t.Fatal("read-only check started an install",err)}
}
func TestSourcePackagePreflightRespectsPinnedEngineAndArchitecture(t *testing.T) {
 oldVersion,oldTransport:=Version,http.DefaultTransport
 defer func(){Version,http.DefaultTransport=oldVersion,oldTransport}()
 Version="0.2.0-alpha.19"
 m,_:=updateFixture(t)
 doc:=fakeSourceRelease()
 archive:=packageTar(t,map[string]string{
    "keenetic-pilot/PLATFORM":runtime.GOARCH+"\n",
    "keenetic-pilot/VERSION":doc.Version+"\n",
    "keenetic-pilot/dependencies/nfqws2.env":"ENGINE_VERSION="+doc.EngineVersion+"\nENGINE_SHA256="+doc.EngineSHA256+"\n",
    "keenetic-pilot/bin/pilot":"#!/bin/sh\nif [ \"$1\" = --version ]; then echo "+doc.Version+"; fi\nexit 0\n",
 })
 h:=sha256.Sum256(archive)
 name:="keenetic-pilot-"+doc.Version+"-linux-"+runtime.GOARCH+".tar.gz"
 manifest:=[]byte(hex.EncodeToString(h[:])+"  "+name+"\n")
 mh:=sha256.Sum256(manifest)
 doc.ManifestSHA256=hex.EncodeToString(mh[:])
 http.DefaultTransport=updateTransport(func(r *http.Request)(*http.Response,error){
    var data []byte
    if strings.HasSuffix(r.URL.Path,"/SHA256SUMS"){data=manifest
    }else if strings.HasSuffix(r.URL.Path,".tar.gz"){data=archive
    }else{t.Fatalf("unexpected release request %s",r.URL.Path)}
    if !strings.Contains(r.URL.Path,"/ssh-releases/"){t.Fatal("unexpected update channel")}
    return &http.Response{StatusCode:200,Body:io.NopCloser(bytes.NewReader(data)),Header:make(http.Header)},nil
 })
 if err:=m.prepareUpdateFrom(context.Background(),t.TempDir(),releaseDocument{Version:doc.Version,ManifestSHA256:doc.ManifestSHA256,EngineVersion:doc.EngineVersion,EngineSHA256:doc.EngineSHA256},sourceReleaseBase);err!=nil {t.Fatal(err)}
 doc.EngineSHA256=strings.Repeat("0",64)
 if err:=m.prepareUpdateFrom(context.Background(),t.TempDir(),releaseDocument{Version:doc.Version,ManifestSHA256:doc.ManifestSHA256,EngineVersion:doc.EngineVersion,EngineSHA256:doc.EngineSHA256},sourceReleaseBase);err==nil {t.Fatal("incompatible engine accepted")}
}
func TestSourceAPIsRequireAuthenticationAndOrigin(t *testing.T) {
 m:=NewManager(DefaultConfig(),filepath.Join(t.TempDir(),"config.json"),&testBackend{},successfulProber{},&LogBuffer{})
 h:=Handler(m,strings.Repeat("a",64))
 for _,item:=range []struct{method,path string}{
  {"GET","/api/updates/sources"},
  {"POST","/api/updates/sources/check"},
  {"POST","/api/updates/sources/install"},
  {"GET","/api/telegram/relay-draft"},
  {"PUT","/api/telegram/relay-draft"},
 } {
    r:=httptest.NewRequest(item.method,"http://router.test"+item.path,strings.NewReader("{}"))
    r.Header.Set("Content-Type","application/json")
    w:=httptest.NewRecorder();h.ServeHTTP(w,r)
    if w.Code!=401 {t.Fatalf("unprotected %s: %d",item.path,w.Code)}
    r=httptest.NewRequest(item.method,"http://router.test"+item.path,strings.NewReader("{}"))
    r.Header.Set("Authorization","Bearer "+strings.Repeat("a",64))
    r.Header.Set("Origin","http://evil.example")
    w=httptest.NewRecorder();h.ServeHTTP(w,r)
    if w.Code!=403 {t.Fatalf("foreign origin %s: %d",item.path,w.Code)}
 }
}

func TestBetaDiscoveryAndAlphaBridge(t *testing.T) {
 oldVersion,oldTransport:=Version,http.DefaultTransport
 defer func(){Version,http.DefaultTransport=oldVersion,oldTransport}()
 Version="0.2.0-alpha.33"
 m,_:=updateFixture(t)
 alpha:=fakeSourceRelease();alpha.Version=Version
 beta:=alpha;beta.Version="0.2.0-beta.1"
 a,_:=json.Marshal(alpha); b,_:=json.Marshal(beta)
 http.DefaultTransport=updateTransport(func(r *http.Request)(*http.Response,error){
  var data []byte
  switch {
  case strings.HasSuffix(r.URL.Path,"/latest-chatgpt.json"),strings.HasSuffix(r.URL.Path,"/v0.2.0-alpha.33/release-info.json"):data=a
  case strings.HasSuffix(r.URL.Path,"/latest-chatgpt-beta.json"),strings.HasSuffix(r.URL.Path,"/v0.2.0-beta.1/release-info.json"):data=b
  default:t.Fatalf("unexpected update path %s",r.URL.Path)
  }
  return &http.Response{StatusCode:200,Body:io.NopCloser(bytes.NewReader(data)),Header:make(http.Header)},nil
 })
 if err:=m.CheckSourceUpdate(context.Background(),"ChatGPT");err!=nil{t.Fatal(err)}
 s:=m.SourceUpdates().Sources[0]
 if !s.Available||s.Latest!=beta.Version{t.Fatal(s)}
}
