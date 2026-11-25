package pilot

import (
 "context"
 "errors"
 "os/exec"
 "strings"
 "time"
)

// vpnHealth is intentionally separate from DPI measurements: an UP interface
// does not prove that it can carry HTTPS traffic.
type vpnHealth struct {
 Interface string `json:"interface"`
 State string `json:"state"`
 CheckedAt time.Time `json:"checked_at"`
}

func probeVPN(ctx context.Context, name string) vpnHealth {
 result:=vpnHealth{Interface:name, State:"unknown", CheckedAt:time.Now()}
 if !geoTunnelRE.MatchString(name){return result}
 // Only a detected Linux interface may be used. Never fall back to WAN.
 actual:=""
 for _,iface:=range geoTunnels(){if strings.EqualFold(iface,name){actual=iface;break}}
 if actual==""{result.State="down";return result}
 if _,err:=exec.LookPath("curl");err!=nil{return result}
 for _,target:=range []string{"https://1.1.1.1/cdn-cgi/trace","https://8.8.8.8/"}{
  if ctx.Err()!=nil{return result}
  attempt,cancel:=context.WithTimeout(ctx,5*time.Second)
  // -q disables curlrc; disable proxy env and redirects. Interface binding
  // uses if! syntax so a failed bind cannot turn into DNS/source selection.
  err:=exec.CommandContext(attempt,"curl","-q","--noproxy","*","--interface","if!"+actual,
   "--connect-timeout","3","--max-time","4","--silent","--output","/dev/null",
   "--write-out","%{http_code}",target).Run()
  cancel()
  if err==nil{result.State="reachable";return result}
  var exit *exec.ExitError
  if !errors.As(err,&exit){return result}
  // Interface binding or unsupported CLI options are not a verified outage.
  if exit.ExitCode()==45||exit.ExitCode()==2{return result}
 }
 if ctx.Err()==nil{result.State="unreachable"}
 return result
}

// Route decisions are pure and can be tested before adding a device-specific
// route writer. A direct path is allowed only by explicit service policy.
type vpnRoute string
const (
 vpnPrimary vpnRoute="primary"
 vpnBackup vpnRoute="backup"
 vpnDPI vpnRoute="dpi"
 vpnBlocked vpnRoute="unavailable"
)
type vpnFailover struct {
 Active vpnRoute
 primaryGood,primaryBad,backupGood,backupBad,dpiGood,dpiBad int
}
func vpnCount(state string,good,bad *int){
 switch state {
 case "reachable": if *good<3{*good++};*bad=0
 case "unreachable","down": if *bad<2{*bad++};*good=0
 default: *good=0;*bad=0
 }
}
// Two failures to abandon a route; three successes to restore the primary.
// Unknown samples cannot authorize direct traffic or a new VPN route.
func (s *vpnFailover) observe(primary,backup,dpi string,allowDPI bool) vpnRoute{
 vpnCount(primary,&s.primaryGood,&s.primaryBad)
 vpnCount(backup,&s.backupGood,&s.backupBad)
 vpnCount(dpi,&s.dpiGood,&s.dpiBad)
 if s.primaryGood>=3{s.Active=vpnPrimary;return s.Active}
 if s.Active==vpnPrimary&&s.primaryBad<2{return s.Active}
 if s.Active==vpnBackup&&s.backupBad<2{return s.Active}
 if s.backupGood>=2{s.Active=vpnBackup;return s.Active}
 if allowDPI&&s.Active==vpnDPI&&s.dpiBad<2{return s.Active}
 if allowDPI&&s.dpiGood>=2{s.Active=vpnDPI;return s.Active}
 s.Active=vpnBlocked
 return s.Active
}
