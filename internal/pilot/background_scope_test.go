package pilot

import (
  "context"
  "os"
  "path/filepath"
  "testing"
  "time"
)

func TestKeeneticAutomaticSearchRestrictsUnrelatedSitesByDefault(t *testing.T){
 m,c,_,s:=activeFixture(t)
 if c.BackgroundOtherDomains {t.Fatal("wide background discovery must be opt-in")}
 other:=DomainScope{"unrelated.example.net","tls13","ipv4",443}
 if autoSearchEligible(c,other,false) {t.Fatal("unknown domain would cause automatic probes")}
 if !autoSearchEligible(c,s,false) {t.Fatal("YouTube lost automatic configured-profile matching")}
 if !autoSearchEligible(c,other,true) {t.Fatal("manual queue blocked by optimization")}
 m.domainTasks=map[DomainScope]*domainTask{
    other:{Scope:other,Signal:time.Now().Unix(),Manual:false},
 }
 if err:=os.WriteFile(filepath.Join(c.RuntimeDir,"state1.tsv"),adaptiveFrame(3,nil,nil),0600);err!=nil{t.Fatal(err)}
 m.activeTick(context.Background())
 if _,exists:=m.domainTasks[other];exists {t.Fatal("old unrelated queued task not cleaned")}
 if m.Snapshot().DomainQueue!=0 {t.Fatal("stale queue shown after pruning",m.Snapshot().DomainQueue)}
 if err:=m.QueueDomain(other);err!=nil{t.Fatal("explicit unknown domain should remain actionable",err)}
 if !m.domainTasks[other].Manual {t.Fatal("user-requested queue entry not distinguished from background")}
 triggered:=0
 m.activeProbeHook=func(_ context.Context,_ Config,scope DomainScope,_ Target,_ string)activeEvidence{
     if scope!=other{t.Fatal("different scope was probed",scope)}
     triggered++
     return activeEvidence{Local:true,Result:ProbeResult{Status:403,Error:"HTTP rejected"}}
 }
 m.activeTick(context.Background())
 if triggered==0 {t.Fatal("manual search incorrectly suppressed")}
 if m.Snapshot().DomainJob.Scope!=other {t.Fatal("manual search not surfaced")}
}
func TestKeeneticWideBackgroundDiscoveryRequiresExplicitConsent(t *testing.T){
 m,c,_,s:=activeFixture(t)
 other:=DomainScope{"unrelated.example.net","tls13","ipv4",443}
 m.domainTasks=map[DomainScope]*domainTask{other:{Scope:other,Signal:time.Now().Unix()}}
 if err:=os.WriteFile(filepath.Join(c.RuntimeDir,"state1.tsv"),adaptiveFrame(3,nil,nil),0600);err!=nil{t.Fatal(err)}
 m.c.BackgroundOtherDomains=true
 if !autoSearchEligible(m.Config(),other,false) {t.Fatal("enabled opt-in ignored")}
 m.activeProbeHook=func(_ context.Context,_ Config,scope DomainScope,_ Target,_ string)activeEvidence{
   if scope!=other{t.Fatal(scope)}
   return activeEvidence{Local:true,Result:ProbeResult{Status:451,Error:"HTTP rejection"}}
 }
 m.activeTick(context.Background())
 if m.domainJob.Scope!=other{t.Fatal("broad discovery did not process its existing queue")}
 // Turning the flag off invalidates only automatic unknown tasks, not saved
 // plans, configured services, or manually requested checks.
 m.c.BackgroundOtherDomains=false
 m.domainTasks[other].Signal=time.Now().Unix()
 m.activeTick(context.Background())
 if _,ok:=m.domainTasks[other];ok {t.Fatal("opt-out did not prune automatic unknown tasks")}
 if servicePriority(m.Config(),s)!=0 {t.Fatal("configured service lost priority")}
}
