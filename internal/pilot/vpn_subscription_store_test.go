package pilot
import("testing";"path/filepath";"os";"time")
func TestSavedSubscriptionPrivateFile(t *testing.T){
 path:=filepath.Join(t.TempDir(),"subscription.json")
 s:=savedSubscription{URL:"https://example.com/test",Data:[]byte("ss://test"),Updated:time.Now()}
 if e:=WriteJSON(path,s);e!=nil{t.Fatal(e)}
 got,e:=readSavedSubscription(path);if e!=nil||got.URL!=s.URL||string(got.Data)!=string(s.Data){t.Fatalf("read failed %v",e)}
 if e=os.Chmod(path,0644);e!=nil{t.Fatal(e)}
 if _,e=readSavedSubscription(path);e==nil{t.Fatal("accepted non-private file")}
}
