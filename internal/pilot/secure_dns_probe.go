package pilot

import (
 "context"
 "net"
 "os/exec"
 "strings"
 "time"
)

type secureDNSResult struct {
 Protocol string `json:"protocol"`
 Server string `json:"server"`
 Status string `json:"status"`
 Milliseconds int64 `json:"milliseconds"`
}

// Fixed public test name and resolver, never arbitrary command arguments.
// kdig validates the certificate chain and hostname for every transport.
func secureDNSProbe(ctx context.Context) []secureDNSResult {
 out:=[]secureDNSResult{}
 path,err:=exec.LookPath("kdig")
 for _,p:=range []struct{name,option,port string}{{"DoH","+https","443"},{"DoT","+tls","853"},{"DoQ","+quic","853"}} {
  r:=secureDNSResult{Protocol:p.name,Server:"dns.adguard-dns.com",Status:"unavailable"}
  if err==nil {
   start:=time.Now()
   qctx,cancel:=context.WithTimeout(ctx,5*time.Second)
   b,e:=exec.CommandContext(qctx,path,"@94.140.14.14","-p",p.port,p.option,"+tls-ca","+tls-host=dns.adguard-dns.com","+timeout=3","+retry=0","+short","example.com","A").Output()
   r.Milliseconds=time.Since(start).Milliseconds()
   r.Status="failed"
   if qctx.Err()!=nil { r.Status="timeout" } else if e==nil && dnsProbeAnswer(string(b)) { r.Status="ok" }
   cancel()
  }
  out=append(out,r)
 }
 return out
}
func dnsProbeAnswer(s string) bool {
 for _,line:=range strings.Split(s,"\n") {
  ip:=net.ParseIP(strings.TrimSpace(line))
  if ip!=nil && ip.To4()!=nil && !ip.IsUnspecified() && !ip.IsLoopback() {return true}
 }
 return false
}
