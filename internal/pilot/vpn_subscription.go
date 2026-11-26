package pilot

import (
 "context"
 "encoding/base64"
 "errors"
 "io"
 "net"
 "net/http"
 "net/netip"
 "net/url"
 "strings"
 "time"
)

const maxSubscriptionBytes = 512*1024

type subscriptionPreview struct {
 Total int `json:"total"`
 Types map[string]int `json:"types"`
 Unknown int `json:"unknown"`
}

func previewSubscription(data []byte) (subscriptionPreview,error) {
 p:=subscriptionPreview{Types:map[string]int{}}
 if len(data)>maxSubscriptionBytes {return p,errors.New("подписка слишком большая")}
 s:=strings.TrimSpace(string(data))
 if !strings.Contains(s,"://") {
  compact:=strings.Join(strings.Fields(s),"")
  for _, enc:=range []*base64.Encoding{base64.StdEncoding,base64.RawStdEncoding,base64.URLEncoding,base64.RawURLEncoding} {
   if decoded,e:=enc.DecodeString(compact);e==nil && strings.Contains(string(decoded),"://"){s=string(decoded);break}
  }
 }
 known:=map[string]bool{"vless":true,"vmess":true,"trojan":true,"ss":true,"ssr":true,"hysteria2":true,"hy2":true,"tuic":true,"wireguard":true}
 for _,line:=range strings.Split(s,"\n"){
  line=strings.TrimSpace(line)
  if line==""||strings.HasPrefix(line,"#"){continue}
  if p.Total>=2000{return p,errors.New("слишком много записей")}
  p.Total++
  scheme,rest,ok:=strings.Cut(line,"://")
  scheme=strings.ToLower(scheme)
  if !ok||!known[scheme]||strings.TrimSpace(rest)==""{p.Unknown++;continue}
  if scheme=="hy2"{scheme="hysteria2"}
  p.Types[scheme]++
 }
 if len(p.Types)==0{return p,errors.New("не найдены поддерживаемые форматы ссылок; YAML и JSON пока не поддерживаются")}
 return p,nil
}

// Restrict outbound preview to public IPv4 HTTPS, without redirects, proxy,
// cookies, or forwarding router authentication. Never return raw remote content.
func subscriptionPublicIP(ip net.IP) bool {
 a,ok:=netip.AddrFromSlice(ip)
 if !ok{return false};a=a.Unmap()
 if !a.Is4()||!publicIP(net.IP(a.AsSlice())){return false}
 for _,block:=range []string{"0.0.0.0/8","192.0.0.0/24","192.0.2.0/24","192.88.99.0/24","198.51.100.0/24","203.0.113.0/24","224.0.0.0/3","169.254.0.0/16","100.64.0.0/10","198.18.0.0/15"} {
  if netip.MustParsePrefix(block).Contains(a){return false}
 }
 return true
}
func fetchSubscriptionPreview(parent context.Context, raw string) (subscriptionPreview,error) {
 empty:=subscriptionPreview{}
 u,err:=url.Parse(strings.TrimSpace(raw))
 if err!=nil||len(raw)>4096||u.Scheme!="https"||u.User!=nil||u.Hostname()==""||u.Fragment!=""||(u.Port()!=""&&u.Port()!="443"){
  return empty,errors.New("нужна HTTPS-ссылка подписки на порту 443 без логина и пароля в адресе")
 }
 ctx,cancel:=context.WithTimeout(parent,12*time.Second);defer cancel()
 transport:=&http.Transport{DisableKeepAlives:true,ResponseHeaderTimeout:5*time.Second,MaxResponseHeaderBytes:16384,
 DialContext:func(ctx context.Context,network,address string)(net.Conn,error){
  host,port,e:=net.SplitHostPort(address);if e!=nil||port!="443"{return nil,errors.New("некорректное назначение")}
  ips,e:=net.DefaultResolver.LookupIP(ctx,"ip4",host);if e!=nil||len(ips)==0||len(ips)>32{return nil,errors.New("DNS подписки недоступен")}
  for _,ip:=range ips{if !subscriptionPublicIP(ip){return nil,errors.New("локальный или зарезервированный адрес запрещён")}}
  d:=net.Dialer{Timeout:4*time.Second}
  for _,ip:=range ips{conn,e:=d.DialContext(ctx,"tcp4",net.JoinHostPort(ip.String(),"443"));if e==nil{return conn,nil}}
  return nil,errors.New("подключение недоступно")
 }}
 defer transport.CloseIdleConnections()
 client:=&http.Client{Transport:transport,CheckRedirect:func(*http.Request,[]*http.Request)error{return http.ErrUseLastResponse}}
 req,e:=http.NewRequestWithContext(ctx,http.MethodGet,u.String(),nil);if e!=nil{return empty,errors.New("некорректный запрос")}
 response,e:=client.Do(req);if e!=nil{return empty,errors.New("не удалось получить подписку по защищённому HTTPS")}
 defer response.Body.Close()
 if response.StatusCode!=200{return empty,errors.New("сервер подписки не вернул HTTP 200; перенаправления запрещены")}
 data,e:=io.ReadAll(io.LimitReader(response.Body,maxSubscriptionBytes+1))
 if e!=nil{return empty,errors.New("не удалось прочитать подписку")}
 return previewSubscription(data)
}
