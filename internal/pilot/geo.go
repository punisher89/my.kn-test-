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
}
var geoServices = []GeoService{
 {"openai","ChatGPT / OpenAI",[]string{"chatgpt.com","openai.com","oaistatic.com","oaiusercontent.com"}},
 {"claude","Claude",[]string{"claude.ai","anthropic.com","claudeusercontent.com"}},
 {"gemini","Gemini",[]string{"gemini.google.com","aistudio.google.com","generativelanguage.googleapis.com"}},
 {"copilot","Microsoft Copilot",[]string{"copilot.microsoft.com","githubcopilot.com"}},
 {"grok","Grok",[]string{"grok.com","x.ai"}},
 {"instagram","Instagram",[]string{"instagram.com","cdninstagram.com"}},
 {"facebook","Facebook",[]string{"facebook.com","fbcdn.net","messenger.com"}},
 {"threads","Threads",[]string{"threads.net","threads.com"}},
 {"spotify","Spotify",[]string{"spotify.com","scdn.co","spotifycdn.com"}},
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
 }
 if len(selected)>0{return GeoPlan{},errors.New("Неизвестный сервис")}
 out.Commands=strings.Join(commands,"\n")+"\n"
 return out,nil
}
