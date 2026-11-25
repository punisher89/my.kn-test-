package pilot

import "testing"

func TestVPNFailoverHysteresis(t *testing.T){
 var s vpnFailover
 for i:=0;i<3;i++{s.observe("reachable","reachable","reachable",true)}
 if s.Active!=vpnPrimary{t.Fatal(s.Active)}
 if got:=s.observe("down","reachable","reachable",true);got!=vpnPrimary{t.Fatal(got)}
 if got:=s.observe("down","reachable","reachable",true);got!=vpnBackup{t.Fatal(got)}
 for i:=0;i<2;i++{if got:=s.observe("reachable","reachable","reachable",true);got!=vpnBackup{t.Fatal(got)}}
 if got:=s.observe("reachable","reachable","reachable",true);got!=vpnPrimary{t.Fatal(got)}
}

func TestVPNFailoverDirectRequiresPermission(t *testing.T){
 var s vpnFailover
 for i:=0;i<3;i++{s.observe("down","down","reachable",false)}
 if s.Active!=vpnBlocked{t.Fatal(s.Active)}
 if got:=s.observe("down","down","reachable",true);got!=vpnDPI{t.Fatal(got)}
 if got:=s.observe("down","down","reachable",false);got!=vpnBlocked{t.Fatal(got)}
}

func TestVPNFailoverUnknownDoesNotAuthorizeRoute(t *testing.T){
 var s vpnFailover
 for i:=0;i<4;i++{s.observe("unknown","unknown","unknown",true)}
 if s.Active!=vpnBlocked{t.Fatal(s.Active)}
}
