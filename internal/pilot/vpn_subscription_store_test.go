package pilot
import("context";"testing";"path/filepath";"os";"time")
func TestSavedSubscriptionPrivateFile(t *testing.T){
 path:=filepath.Join(t.TempDir(),"subscription.json")
 s:=savedSubscription{URL:"https://example.com/test",Data:[]byte("ss://test"),Updated:time.Now()}
 if e:=WriteJSON(path,s);e!=nil{t.Fatal(e)}
 got,e:=readSavedSubscription(path);if e!=nil||got.URL!=s.URL||string(got.Data)!=string(s.Data){t.Fatalf("read failed %v",e)}
 if e=os.Chmod(path,0644);e!=nil{t.Fatal(e)}
 if _,e=readSavedSubscription(path);e==nil{t.Fatal("accepted non-private file")}
}

func TestLocalSubscriptionImportPreservesPreviousOnInvalidData(t *testing.T){
 m:=NewManager(DefaultConfig(),filepath.Join(t.TempDir(),"config.json"),&testBackend{},successfulProber{},&LogBuffer{})
 data:=[]byte("vless://dummy@example.com\nss://dummy@example.com")
 got,e:=m.ImportSubscriptionFile(data)
 if e!=nil||!got.Saved||!got.Local||got.Preview.Total!=2{t.Fatalf("import: %+v %v",got,e)}
 if _,e=m.ImportSubscriptionFile([]byte("invalid"));e==nil{t.Fatal("invalid file accepted")}
 state,e:=m.SubscriptionState();if e!=nil||!state.Local||state.Preview.Total!=2{t.Fatal("old file lost",e)}
 if _,e=m.SaveSubscription(context.Background(),"",true);e==nil{t.Fatal("local refresh accepted")}
 if e=m.refreshSubscriptionIfDue(context.Background(),time.Now().Add(48*time.Hour));e!=nil{t.Fatal(e)}
}
