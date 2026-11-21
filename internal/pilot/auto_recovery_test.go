package pilot

import (
 "testing"
 "time"
)

func syntheticRecoveryResults(c Config,p Profile,message string,code int) []ProbeResult {
 results:=[]ProbeResult{}
 families:=[]string{"tcp4"}
 if c.IPv6{families=append(families,"tcp6")}
 for _,target:=range p.Targets {
   for _,family:=range families {
     for _,version:=range []string{"1.2","1.3"} {
       for attempt:=1;attempt<=c.Attempts;attempt++ {
         results=append(results,ProbeResult{
            URL:target.URL,TLS:version,Family:family,Attempt:attempt,
            OK:false,Status:code,Error:message,
         })
       }
     }
   }
 }
 return results
}
func TestAutomaticRecoveryOnlyOnCompleteCredibleNetworkFailures(t *testing.T){
 c:=DefaultConfig()
 p:=c.Profiles[0]
 good:=syntheticRecoveryResults(c,p,"read tcp: connection reset by peer",0)
 if !autoRecoverableFailure(good,p,c){t.Fatal("confirmed repeated transport errors did not trigger recovery")}
 for _,msg:=range []string{"i/o timeout","net/http: TLS handshake timeout","unexpected EOF","context deadline exceeded"} {
     r:=syntheticRecoveryResults(c,p,msg,0)
     if !autoRecoverableFailure(r,p,c){t.Fatal("known transport symptom not accepted:",msg)}
 }
 tests:=[]struct{name string;edit func([]ProbeResult)[]ProbeResult}{
    {"dns",func(r []ProbeResult)[]ProbeResult{r[0].Error="DNS: no such host";return r}},
    {"certificate",func(r []ProbeResult)[]ProbeResult{r[0].Error="x509: certificate expired";return r}},
    {"http-403",func(r []ProbeResult)[]ProbeResult{r[0].Status=403;return r}},
    {"http-451",func(r []ProbeResult)[]ProbeResult{r[0].Status=451;return r}},
    {"unexpected-refusal",func(r []ProbeResult)[]ProbeResult{r[0].Error="connection refused";return r}},
    {"network-down",func(r []ProbeResult)[]ProbeResult{r[0].Error="network is unreachable";return r}},
    {"unknown",func(r []ProbeResult)[]ProbeResult{r[0].Error="some error";return r}},
    {"mixed",func(r []ProbeResult)[]ProbeResult{r[0].OK=true;r[0].Error="";return r}},
    {"partial",func(r []ProbeResult)[]ProbeResult{return r[:len(r)-1]}},
    {"duplicate",func(r []ProbeResult)[]ProbeResult{r[1]=r[0];return r}},
    {"mismatched-host",func(r []ProbeResult)[]ProbeResult{r[0].URL="https://unknown.test/";return r}},
    {"mismatched-attempt",func(r []ProbeResult)[]ProbeResult{r[0].Attempt=123;return r}},
 }
 for _,tt:=range tests {t.Run(tt.name,func(t *testing.T){
   altered:=tt.edit(syntheticRecoveryResults(c,p,"i/o timeout",0))
   if autoRecoverableFailure(altered,p,c){t.Fatal("unsafe or partial failures started a disruptive auto tune",tt.name)}
 })}
 c.IPv6=true
 if autoRecoverableFailure(good,p,c){t.Fatal("missing IPv6 results were counted as complete")}
 if !autoRecoverableFailure(syntheticRecoveryResults(c,p,"i/o timeout",0),p,c){t.Fatal("complete IPv4/IPv6 results rejected")}
}
func TestBackgroundCheckSchedulingAvoidsConcurrentProbes(t *testing.T){
 c:=DefaultConfig()
 c.IntervalMinutes=15
 c.Profiles=c.Profiles[:2]
 if got:=backgroundCheckInterval(c);got!=7*time.Minute+30*time.Second {
   t.Fatal("two profiles should be checked on alternating 7.5-minute ticks",got)
 }
 c.IntervalMinutes=2
 c.Profiles=append(c.Profiles,c.Profiles...)
 if got:=backgroundCheckInterval(c);got!=time.Minute {t.Fatal("excessive background probe cadence",got)}
 c.Profiles=nil
 c.IntervalMinutes=15
 if got:=backgroundCheckInterval(c);got!=15*time.Minute {t.Fatal("zero-profile schedule did not stay bounded",got)}
 if automaticTuneCooldown<30*time.Minute || catalogStartDelay<5*time.Minute {
    t.Fatal("unsafe recovery or boot maintenance interval")
 }
}
