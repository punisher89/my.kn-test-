package pilot
import "testing"
func TestDNSProbeInput(t *testing.T){
 for _,in:=range []secureDNSRequest{{Server:"--help"},{Server:"https://dns.example/dns-query"},{Server:"dns.example",Address:"-p 123"},{Server:"dns.example",Protocol:"bogus"}} {if _,err:=normalizeDNSProbe(in);err==nil{t.Fatalf("accepted invalid input: %#v",in)}}
 got,err:=normalizeDNSProbe(secureDNSRequest{Server:"dns.example.",Address:"2001:db8::1",Protocol:"DoQ"});if err!=nil||got.Server!="dns.example"{t.Fatalf("valid request: %#v %v",got,err)}
 if !dnsProbeAnswer("192.0.2.1\n")||dnsProbeAnswer("127.0.0.1\n")||dnsProbeAnswer("error"){t.Fatal("invalid DNS answer handling")}
}
