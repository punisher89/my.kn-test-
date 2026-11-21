package pilot

import "strings"

// Auto-tuning in legacy mode restarts nfqws2 for each candidate. A complete,
// repeated transport failure is necessary before risking a live service.
// HTTP rejections, DNS, cert, device, and mixed results never justify it.
func autoRecoverableFailure(results []ProbeResult, p Profile, c Config) bool {
    want := len(p.Targets)*2*c.Attempts
    if c.IPv6 {want*=2}
    if want<2 || len(results)!=want {return false}
    failures:=0
    for _,r:=range results {
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
    return failures>=2
}

const (
    automaticTuneCooldown = 60 * 60 * 1000000000 // one hour, time.Duration nanoseconds
    catalogStartDelay = 10 * 60 * 1000000000
)
