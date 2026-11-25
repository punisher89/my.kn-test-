package pilot

import (
 "strings"
 "testing"
)

func TestGeoPlanPreviewOnly(t *testing.T){
 p,e:=buildGeoPlan(GeoPlanRequest{Interface:"OpkgTun10",Policy:"Policy0",Services:[]string{"openai","instagram"}})
 if e!=nil||p.Applied||len(p.Groups)!=2{t.Fatal(p,e)}
 for _,s:=range []string{"object-group fqdn pilot-geo-openai"," include chatgpt.com","ip policy Policy0 dns-proxy route object-group pilot-geo-instagram OpkgTun10 auto reject"}{if !strings.Contains(p.Commands,s){t.Fatal(s,p.Commands)}}
 for _,s:=range []string{"ip route default","system configuration save","facebook.com","youtube.com"}{if strings.Contains(p.Commands,s){t.Fatal("unexpected command",s)}}
}
func TestGeoPlanRejectsUntrustedInput(t *testing.T){
 for _,r:=range []GeoPlanRequest{
 {Interface:"ppp0",Policy:"Policy0",Services:[]string{"openai"}},
 {Interface:"OpkgTun10\nexit",Policy:"Policy0",Services:[]string{"openai"}},
 {Interface:"OpkgTun10",Policy:"Policy0;exit",Services:[]string{"openai"}},
 {Interface:"OpkgTun10",Policy:"Policy0",Services:[]string{"openai","openai"}},
 {Interface:"OpkgTun10",Policy:"Policy0",Services:[]string{"bogus"}},
 {Interface:"OpkgTun10",Policy:"Policy0"},
 }{if _,e:=buildGeoPlan(r);e==nil{t.Fatal("accepted",r)}}
}
func TestHybridLightStrategies(t *testing.T){
 seen:=map[string]bool{}
 count:=0
 for _,s:=range Strategies{
  if seen[s.ID]{t.Fatal("duplicate strategy",s.ID)}
  seen[s.ID]=true
  if !strings.HasPrefix(s.ID,"hy-"){continue}
  count++
  if len(s.Actions)!=1||StrategyFamily(s)!="split"{t.Fatal(s)}
  valid:=false
  for _,p:=range actionPatterns{if p.MatchString(s.Actions[0]){valid=true}}
  if !valid{t.Fatal("outside existing action allowlist",s)}
 }
 if count!=16{t.Fatal(count)}
}

func TestTelegramTransparentGeoPlan(t *testing.T){
 p,e:=buildGeoPlan(GeoPlanRequest{Interface:"OpkgTun10",Policy:"Policy0",Services:[]string{"telegram"}})
 if e!=nil||p.Applied{t.Fatal(p,e)}
 if strings.Count(p.Commands,"ip policy Policy0 route ")!=9||strings.Count(p.Commands,"ip policy Policy0 ipv6 route ")!=5{t.Fatal(p.Commands)}
 for _,line:=range []string{"ip policy Policy0 route 149.154.160.0 255.255.240.0 OpkgTun10 auto reject","ip policy Policy0 ipv6 route 2001:b28:f23d::/48 OpkgTun10 auto reject","ip policy Policy0 dns-proxy route object-group pilot-geo-telegram OpkgTun10 auto reject"}{if !strings.Contains(p.Commands,line){t.Fatal(line,p.Commands)}}
 for _,forbidden:=range []string{"ip route default","tg://","mtproto","system configuration save","iptables"}{if strings.Contains(p.Commands,forbidden){t.Fatal(forbidden)}}
 other,e:=buildGeoPlan(GeoPlanRequest{Interface:"OpkgTun10",Policy:"Policy0",Services:[]string{"openai"}})
 if e!=nil||strings.Contains(other.Commands,"149.154.160.0"){t.Fatal(other,e)}
}
