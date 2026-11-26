package pilot
import (
 "net/http/httptest"
 "testing"
)
func TestSSHPasswordPreflight(t *testing.T){
 good:=sshPasswordRequest{Host:"example.org",User:"root",Port:"22",Password:"some password"}
 if validateSSHPassword(good)!=nil{t.Fatal("valid request rejected")}
 bad:=good;bad.Host="-oProxyCommand=bad"
 if validateSSHPassword(bad)==nil{t.Fatal("invalid host accepted")}
 bad=good;bad.Password="first\nsecond"
 if validateSSHPassword(bad)==nil{t.Fatal("multiline password accepted")}
 r:=httptest.NewRequest("POST","http://192.168.1.1/api/vpn/ssh-check",nil)
 r.RemoteAddr="192.168.1.2:1234"
 if sshSecretTransport(r){t.Fatal("LAN HTTP accepted")}
 r=httptest.NewRequest("POST","http://localhost:19080/api/vpn/ssh-check",nil)
 r.RemoteAddr="127.0.0.1:1234"
 if !sshSecretTransport(r){t.Fatal("loopback forward rejected")}
}
