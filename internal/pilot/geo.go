package pilot

import (
 "errors"
 "fmt"
 "net"
 "regexp"
 "sort"
 "strings"
)

type GeoService struct {
 ID string `json:"id"`
 Name string `json:"name"`
 Domains []string `json:"domains"`
 Networks []string `json:"networks,omitempty"`
}
// Snapshot: https://core.telegram.org/resources/cidr.txt (2026-11-26).
var telegramNetworks = []string{
 "91.108.56.0/22","91.108.4.0/22","91.108.8.0/22","91.108.16.0/22","91.108.12.0/22","149.154.160.0/20","91.105.192.0/23","91.108.20.0/22","185.76.151.0/24",
 "2001:b28:f23d::/48","2001:b28:f23f::/48","2001:67c:4e8::/48","2001:b28:f23c::/48","2a0a:f280::/32",
}
var geoServices = []GeoService{
 {"telegram","Telegram · сквозная маршрутизация",[]string{"telegram.org","t.me","telegram.me","telegra.ph","telesco.pe"},telegramNetworks},
 {"openai","ChatGPT / OpenAI",[]string{"chatgpt.com","openai.com","oaistatic.com","oaiusercontent.com"},nil},
 {"claude","Claude",[]string{"claude.ai","anthropic.com","claudeusercontent.com"},nil},
 {"gemini","Gemini",[]string{"gemini.google.com","aistudio.google.com","generativelanguage.googleapis.com"},nil},
 {"copilot","Microsoft Copilot",[]string{"copilot.microsoft.com","githubcopilot.com"},nil},
 {"grok","Grok",[]string{"grok.com","x.ai"},nil},
 {"instagram","Instagram",[]string{"instagram.com","cdninstagram.com"},nil},
 {"facebook","Facebook",[]string{"facebook.com","fbcdn.net","messenger.com"},nil},
 {"threads","Threads",[]string{"threads.net","threads.com"},nil},
 {"spotify","Spotify",[]string{"spotify.com","scdn.co","spotifycdn.com"},nil},
}
type GeoPlanRequest struct {
 Interface string `json:"interface"`
 Policy string `json:"policy"`
 Services []string `json:"services"`
}
type GeoPlan struct {
 Commands string `json:"commands"`
 Groups []string `json:"groups"`
 Applied bool `json:"applied"`
}
var geoNameRE=regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,31}$`)
var geoTunnelRE=regexp.MustCompile(`(?i)^(opkgtun|wireguard|amneziawg|openvpn)[0-9]{1,4}$`)
func geoTunnels() []string {
 result:=[]string{}
 ifaces,_:=net.Interfaces()
 for _,iface:=range ifaces {
  if iface.Flags&net.FlagUp!=0 && geoTunnelRE.MatchString(iface.Name){result=append(result,iface.Name)}
 }
 sort.Strings(result)
 return result
}
func buildGeoPlan(r GeoPlanRequest) (GeoPlan,error) {
 out:=GeoPlan{Groups:[]string{},Applied:false}
 if !geoTunnelRE.MatchString(r.Interface)||!geoNameRE.MatchString(r.Policy){return out,errors.New("Укажите имя VPN-интерфейса и существующей политики KeeneticOS 5.2.")}
 if len(r.Services)==0||len(r.Services)>len(geoServices){return out,errors.New("Выберите хотя бы один сервис.")}
 selected:=map[string]bool{}
 for _,id:=range r.Services{
  if selected[id]{return out,errors.New("Повтор сервиса")}
  selected[id]=true
 }
 var commands []string
 for _,s:=range geoServices{
  if !selected[s.ID]{continue}
  delete(selected,s.ID)
  group:="pilot-geo-"+s.ID
  out.Groups=append(out.Groups,group)
  commands=append(commands,"object-group fqdn "+group)
  for _,d:=range s.Domains{commands=append(commands," include "+d)}
  commands=append(commands," exit",fmt.Sprintf("ip policy %s dns-proxy route object-group %s %s auto reject",r.Policy,group,r.Interface))
  for _,prefix:=range s.Networks{
   ip,network,err:=net.ParseCIDR(prefix)
   if err!=nil{return GeoPlan{},errors.New("Некорректная встроенная сеть")}
   if ip.To4()!=nil{
    commands=append(commands,fmt.Sprintf("ip policy %s route %s %s %s auto reject",r.Policy,network.IP.String(),net.IP(network.Mask).String(),r.Interface))
   }else{
    commands=append(commands,fmt.Sprintf("ip policy %s ipv6 route %s %s auto reject",r.Policy,network.String(),r.Interface))
   }
  }
 }
 if len(selected)>0{return GeoPlan{},errors.New("Неизвестный сервис")}
 out.Commands=strings.Join(commands,"\n")+"\n"
 return out,nil
}
