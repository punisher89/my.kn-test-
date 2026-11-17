package pilot

import (
 "strings"
 "testing"
)

func openwrtTestConfig() Config {
 c:=DefaultConfig()
 c.WAN="pppoe-wan"
 c.PPE=false
 c.QUIC=false
 c.Adaptive=false
 c.AdaptiveActive=false
 c.Enabled=false
 return c
}
func TestOpenWrtNftRulesIsolatedFailOpenAndBounded(t *testing.T) {
 c:=openwrtTestConfig()
 rules,err:=OpenWrtNftRules(c)
 if err!=nil{t.Fatal(err)}
 for _,required:=range []string{
  "add table inet keenetic_pilot",
  "pilot_pre { type filter hook prerouting",
  "pilot_post { type filter hook postrouting",
  `oifname "pppoe-wan"`,
  `iifname "pppoe-wan"`,
  "meta nfproto ipv4",
  "ct direction original ct original packets <= 20",
  "ct direction reply ct reply packets <= 20",
  "tcp dport { 80, 443",
  "tcp sport { 80, 443",
  "queue num 220 bypass",
  `comment "keenetic-pilot:openwrt"`,
 }{
  if !strings.Contains(rules,required) {t.Fatalf("missing %q: %s",required,rules)}
 }
 for _,notAllowed:=range []string{"flush ruleset","flush table","delete table","table inet fw4","-j PPE","iptables","50000-50099","udp dport"} {
  if strings.Contains(rules,notAllowed) {t.Fatalf("unexpected mutation/UDP %q",notAllowed)}
 }
 if strings.Count(rules,"queue num")!=2 {t.Fatal("unexpected queue count",rules)}
}
func TestOpenWrtRuleOptionalProtoAndWANValidation(t *testing.T) {
 c:=openwrtTestConfig()
 c.TelegramMTProto=true;c.DiscordVoice=true;c.QUIC=true;c.IPv6=true
 got,err:=OpenWrtNftRules(c)
 if err!=nil{t.Fatal(err)}
 for _,required:=range []string{"5222","udp dport { 443, 50000-50099 }", "udp sport { 443, 50000-50099 }","queue num 220 bypass","ct direction reply"} {
  if !strings.Contains(got,required) {t.Fatalf("missing %q",required)}
 }
 if strings.Contains(got,"meta nfproto ipv4"){t.Fatal("IPv6 enabled but nft explicitly filters it")}
 c.PPE=true
 if _,err:=OpenWrtNftRules(c);err==nil{t.Fatal("unsafe Keenetic PPE was accepted")}
 c.PPE=false;c.Adaptive=true;c.AdaptiveActive=true
 if _,err:=OpenWrtNftRules(c);err==nil{t.Fatal("unimplemented adaptive mark path accepted")}
 c.AdaptiveActive=false;c.WAN="wan\"; delete table inet fw4; #"
 if _,err:=OpenWrtNftRules(c);err==nil{t.Fatal("WAN injection not rejected")}
}
func TestOpenWrtRouterDoesNotStartWithoutRealOpenWrt(t *testing.T) {
 if IsOpenWrt(){t.Skip("developer test runs only off an OpenWrt router")}
 r:=NewOpenWrtRouter(&LogBuffer{})
 c:=openwrtTestConfig()
 if err:=r.Start(c);err==nil{t.Fatal("OpenWrt backend started on other platform")}
 if r.Running(){t.Fatal("backend falsely reports running")}
 if err:=r.Stop();err!=nil{t.Fatal("idle backend stop",err)}
 if r.Kind()!="openwrt"{t.Fatal(r.Kind())}
}
