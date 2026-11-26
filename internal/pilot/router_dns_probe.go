package pilot

import (
 "bufio"
 "context"
 "net"
 "net/url"
 "os"
 "os/exec"
 "strings"
 "time"
)

// Configuration inventory is distinct from live DNS answers. Only supported
// global DNS-proxy upstream entries are shown; no full configuration or URL
// paths, credentials, query strings, or filter profile secrets leave this code.
func configuredDNS(raw string) []secureDNSResult {
 var out []secureDNSResult
 seen:=map[string]bool{}
 block:=false
 scanner:=bufio.NewScanner(strings.NewReader(raw))
 scanner.Buffer(make([]byte,4096),256<<10)
 for scanner.Scan(){
  line:=scanner.Text()
  fields:=strings.Fields(line)
  if len(fields)==0{continue}
  if fields[0]=="dns-proxy" {
   block=len(fields)==1
   fields=fields[1:]
  } else if !block||len(line)==len(strings.TrimLeft(line," \t"))||fields[0]=="exit"{
   block=false;continue
  }
  if len(fields)<3||fields[1]!="upstream"{continue}
  var protocol,server string
  endpoint:=strings.Trim(fields[2],"\"'")
  switch fields[0] {
  case "https":
   u,e:=url.Parse(endpoint)
   if e!=nil||u.Scheme!="https"||u.Hostname()==""||u.User!=nil{continue}
   protocol,server="DoH",u.Hostname()
  case "tls","quic","doq":
   protocol="DoT"
   if fields[0]!="tls"{protocol="DoQ"}
   server=endpoint
   for i:=3;i+1<len(fields);i++{if fields[i]=="sni" {server=strings.Trim(fields[i+1],"\"'");break}}
  default:continue
  }
  if net.ParseIP(server)==nil{
   if len(server)>253{continue}
   valid:=true
   for _,l:=range strings.Split(strings.TrimSuffix(server,"."),"."){if !dnsProbeLabel.MatchString(l){valid=false;break}}
   if !valid{continue}
  }
  key:=protocol+" "+server
  if seen[key]{continue}
  seen[key]=true
  out=append(out,secureDNSResult{Protocol:protocol,Server:server,Status:"configured",Error:"Настроен в KeeneticOS; использование и доступность отдельно не подтверждены"})
  if len(out)>=32{break}
 }
 return out
}

func readRouterDNSConfig(ctx context.Context) []secureDNSResult {
 path,err:=exec.LookPath("ndmc")
 if err!=nil {for _,p:=range []string{"/usr/bin/ndmc","/bin/ndmc","/sbin/ndmc","/usr/sbin/ndmc"}{if st,e:=os.Stat(p);e==nil&&st.Mode().IsRegular()&&st.Mode().Perm()&0111!=0{path=p;err=nil;break}}}
 unknown:=func(message string)[]secureDNSResult{return []secureDNSResult{{Protocol:"KeeneticOS",Server:"Настройки DNS",Status:"unknown",Error:message}}}
 if err!=nil{return unknown("CLI ndmc не найден; точные upstream-серверы не определены")}
 qctx,cancel:=context.WithTimeout(ctx,4*time.Second)
 defer cancel()
 b,err:=exec.CommandContext(qctx,path,"-c","show running-config").Output()
 if err!=nil||len(b)>4<<20{return unknown("Не удалось прочитать конфигурацию через ndmc")}
 out:=configuredDNS(string(b))
 if len(out)==0{return unknown("Явные глобальные DoH/DoT/DoQ не распознаны; возможны профиль, DNS провайдера или другая версия CLI")}
 return out
}

func resolvServers(raw string) []string {
 var out []string
 for _,line:=range strings.Split(raw,"\n"){
  f:=strings.Fields(line)
  if len(f)<2||f[0]!="nameserver"{continue}
  ip:=net.ParseIP(f[1])
  if ip==nil||ip.IsUnspecified()||ip.IsMulticast(){continue}
  value:=ip.String()
  found:=false
  for _,v:=range out{if v==value{found=true;break}}
  if !found{out=append(out,value)}
  if len(out)==4{break}
 }
 return out
}

func lookupDNSAt(ctx context.Context, address, protocol string) secureDNSResult {
 r:=secureDNSResult{Protocol:protocol,Server:address,Status:"failed"}
 start:=time.Now()
 qctx,cancel:=context.WithTimeout(ctx,3*time.Second)
 defer cancel()
 // Explicit endpoint: Go DNS client uses UDP and retries with TCP on a
 // truncated response. No kdig, and no substitution of public DNS servers.
 resolver:=&net.Resolver{PreferGo:true,Dial:func(c context.Context,network,_ string)(net.Conn,error){
  return (&net.Dialer{}).DialContext(c,network,net.JoinHostPort(address,"53"))
 }}
 ips,err:=resolver.LookupIP(qctx,"ip4","example.com.")
 if err==nil{for _,ip:=range ips{if dnsProbeAnswer(ip.String()){r.Status="ok";break}}}
 if qctx.Err()!=nil{r.Status="timeout"}
 if r.Status!="ok"{r.Error="Нет подтверждённого A-ответа от указанного резолвера"}
 r.Milliseconds=time.Since(start).Milliseconds()
 return r
}

func routerDNSProbe(ctx context.Context) []secureDNSResult {
 var out []secureDNSResult
 // Check the actual router listener (LAN address if detected, otherwise
 // loopback). Separately show explicit resolv.conf servers when different.
 lan:=DetectNetwork().LAN
 targets:=[]string{"127.0.0.1"}
 if net.ParseIP(lan)!=nil{targets=[]string{lan,"127.0.0.1"}}
 seen:=map[string]bool{}
 for _,ip:=range targets {
  row:=lookupDNSAt(ctx,ip,"DNS роутера")
  out=append(out,row);seen[ip]=true
  if row.Status=="ok"{break}
 }
 if raw,err:=os.ReadFile("/etc/resolv.conf");err==nil{
  for _,ip:=range resolvServers(string(raw)){
   if seen[ip]{continue}
   out=append(out,secureDNSResult{Protocol:"DNS ОС",Server:ip,Status:"configured",Error:"Указан в /etc/resolv.conf; не обязательно upstream Keenetic"})
  }
 }else{out=append(out,secureDNSResult{Protocol:"DNS ОС",Server:"/etc/resolv.conf",Status:"unknown",Error:"Не удалось прочитать список резолверов"})}
 return append(out,readRouterDNSConfig(ctx)...)
}
