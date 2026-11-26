package pilot
import("encoding/base64";"net";"testing")
func TestSubscriptionPreview(t *testing.T){
 data:="vless://test@example.com:443\nss://example\nnot-a-link\n"
 for _,s:=range []string{data,base64.StdEncoding.EncodeToString([]byte(data))}{
 p,e:=previewSubscription([]byte(s));if e!=nil||p.Total!=3||p.Types["vless"]!=1||p.Types["ss"]!=1||p.Unknown!=1{t.Fatalf("unexpected %+v %v",p,e)}
 }
}
func TestSubscriptionPublicIP(t *testing.T){
 for _,s:=range []string{"127.0.0.1","10.0.0.1","192.168.1.1","100.64.0.1","169.254.169.254","198.18.0.1","0.1.2.3","::1","fd00::1","192.0.2.1"}{if subscriptionPublicIP(net.ParseIP(s)){t.Fatalf("accepted %s",s)}}
 if !subscriptionPublicIP(net.ParseIP("1.1.1.1")){t.Fatal("public address rejected")}
}
