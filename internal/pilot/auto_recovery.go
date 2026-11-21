package pilot

import (
 "fmt"
 "strings"
 "time"
)

// Auto-tuning in legacy mode restarts nfqws2 for each candidate. A complete,
// repeated transport failure is necessary before risking a live service.
// HTTP rejections, DNS, cert, device, and mixed results never justify it.
func autoRecoverableFailure(results []ProbeResult, p Profile, c Config) bool {
    want := len(p.Targets)*2*c.Attempts
    if c.IPv6 {want*=2}
    if want<2 || len(results)!=want {return false}
    expected:=map[string]bool{}
    families:=[]string{"tcp4"}
    if c.IPv6 {families=append(families,"tcp6")}
    key:=func(url,tls,family string,attempt int)string {
        return fmt.Sprintf("%s\\x00%s\\x00%s\\x00%d",url,tls,family,attempt)
    }
    for _,t:=range p.Targets {
        for _,family:=range families {
            for _,tls:=range []string{"1.2","1.3"} {
                for attempt:=1;attempt<=c.Attempts;attempt++ {
                    expected[key(t.URL,tls,family,attempt)]=true
                }
            }
        }
    }
    if len(expected)!=want {return false}
    failures:=0
    for _,r:=range results {
        k:=key(r.URL,r.TLS,r.Family,r.Attempt)
        if !expected[k] {return false}
        delete(expected,k)

        if r.OK || r.Status!=0 {return false}
        msg:=strings.ToLower(r.Error)
        for _,reject:=range []string{
            "dns:", "no such host", "непубличн", "certificate", "x509:",
            "network is unreachable", "no route to host", "connection refused",
            "permission denied", "operation not permitted", "address family",
            "protocol not available", "cannot assign requested address",
            "body", "ожидался", "timeout awaiting response headers",
        } {
            if strings.Contains(msg,reject) {return false}
        }
        valid:=false
        for _,symptom:=range []string{
            "connection reset by peer", "read: connection reset",
            "tls handshake timeout", "i/o timeout",
            "context deadline exceeded", "unexpected eof", "eof",
            "connection timed out",
        } {
            if strings.Contains(msg,symptom) {valid=true;break}
        }
        if !valid {return false}
        failures++
    }
    return failures>=2 && len(expected)==0
}

const (
    automaticTuneCooldown = time.Hour
    catalogStartDelay = 10 * time.Minute
)

// Spread background HTTPS checks across service profiles instead of launching
// the whole matrix for every profile in one watchdog tick.
func backgroundCheckInterval(c Config) time.Duration {
    n:=len(c.Profiles)
    if n<1 {n=1}
    gap:=time.Duration(c.IntervalMinutes)*time.Minute/time.Duration(n)
    if gap<time.Minute {gap=time.Minute}
    return gap
}
