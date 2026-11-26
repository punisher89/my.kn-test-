package pilot

import "testing"

func TestRouterDNSInventory(t *testing.T){
 raw:="system name test\n"+
 "dns-proxy\n https upstream https://actual.example/private-token?key=secret dnsm\n tls upstream 192.0.2.1 853 sni dot.example\n quic upstream 192.0.2.2 sni doq.example\n exit\n"+
 "dns-proxy https upstream https://actual.example/other\n"+
 "interface Home\n https upstream https://ignore.example/dns-query\n"
 rows:=configuredDNS(raw)
 if len(rows)!=3||rows[0].Server!="actual.example"||rows[1].Server!="dot.example"||rows[2].Server!="doq.example"||rows[0].Status!="configured"{t.Fatalf("unexpected rows: %#v",rows)}
 if len(configuredDNS("dns-proxy filter profile x https upstream https://ignore.example/dns-query"))!=0{t.Fatal("profile treated as global")}
 a:=resolvServers("nameserver 127.0.0.1\nnameserver 127.0.0.1\nnameserver 2001:db8::1\nnameserver bad\n")
 if len(a)!=2||a[0]!="127.0.0.1"||a[1]!="2001:db8::1"{t.Fatalf("unexpected servers: %v",a)}
}
func TestDNSNoDefaultProvider(t *testing.T){
 in,e:=normalizeDNSProbe(secureDNSRequest{})
 if e!=nil||in.Protocol!="router"||in.Server!=""||in.Address!=""{t.Fatalf("unexpected default: %#v %v",in,e)}
 if _,e=normalizeDNSProbe(secureDNSRequest{Protocol:"DoH"});e==nil{t.Fatal("missing endpoint accepted")}
}
