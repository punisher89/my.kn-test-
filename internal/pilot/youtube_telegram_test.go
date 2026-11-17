package pilot

import (
    "context"
    "os"
    "path/filepath"
    "strings"
    "testing"
    "time"
)

func TestYouTubeStableProtectsExistingMethodAndBypassesBadMediaPlans(t *testing.T) {
    m, c, _, s := activeFixture(t)
    c.YouTubeStable = true
    c.Profiles[0].Strategy = "fake-host-md5"
    if !stableYouTubeHost(c, "www.youtube.com") || !stableYouTubeHost(c, "rr3---sn-x.googlevideo.com") {
        t.Fatal("expected YouTube profile host to be protected")
    }
    if stableYouTubeHost(c, "discord.com") || stableYouTubeHost(c, "youtube.com.evil.example") {
        t.Fatal("other site was mistakenly protected")
    }
    calls := 0
    m.activeProbeHook = func(_ context.Context, _ Config, _ DomainScope, _ Target, _ string) activeEvidence {
        calls++
        return evidence(true, true)
    }
    m.domainJob = &DomainSearch{Scope:s,Started:time.Now(),Trials:[]Trial{}}
    stage, _ := m.searchDomain(context.Background(),c,s)
    if stage != "unmeasured" || m.domainJob.Reason != "video_url_required" || calls != 0 {
        t.Fatalf("auto search modified protected YouTube: job=%+v calls=%d",m.domainJob,calls)
    }
    if err:=m.commitDomain(c,s,"none");err==nil {
        t.Fatal("generic HTTPS was allowed to replace YouTube strategy")
    }
    now:=time.Now().UTC()
    m.domainPlans.Plans=[]DomainPlan{{
        Scope:s, Strategy:"none", Fingerprint:domainFingerprint(c,s),
        Verified:now, Expires:now.Add(time.Hour),
    }}
    if _,ok:=m.planFor(c,s);ok { t.Fatal("stale URL-only plan survived stable mode") }
    if strings.Contains(domainPlansText(c,m.domainPlans.Plans),"www.youtube.com") {
        t.Fatal("unverified legacy CDN plan published in the runtime")
    }
    if got:=profileForDomain(c,"rr3---sn-x.googlevideo.com").Strategy;got!="fake-host-md5" {
        t.Fatal("profile fallback changed",got)
    }
    if err:=m.QueueDomain(s);err==nil || !strings.Contains(err.Error(),"стабильном") {
        t.Fatal("manual queue bypassed stable lock",err)
    }
}

func TestYouTubeStableConfigSurvivesDiskRoundTrip(t *testing.T) {
    c:=DefaultConfig()
    c.YouTubeStable=true
    c.Profiles[0].Strategy="fake-host-md5"
    path:=filepath.Join(t.TempDir(),"config.json")
    if err:=WriteJSON(path,c);err!=nil {t.Fatal(err)}
    got,err:=LoadConfig(path);if err!=nil {t.Fatal(err)}
    if !got.YouTubeStable || got.Profiles[0].Strategy!="fake-host-md5" {
        t.Fatal("recovery setting did not persist")
    }
    _=os.Remove(path)
}

func TestTelegramMTProtoOptInDoesNotLeakToOtherServices(t *testing.T) {
    c:=DefaultConfig()
    basic:=strings.Join(EngineArgs(c)," ")
    if strings.Contains(basic,"--filter-l7=mtproto") || strings.Contains(basic,"--payload=mtproto_initial") {
        t.Fatal("native MTProto enabled without consent")
    }
    for _,r:=range FirewallRules(c) {
        if strings.Contains(strings.Join(r.Args," "),"5222") { t.Fatal("MTProto port leaked to baseline firewall") }
    }
    c.TelegramMTProto=true
    if err:=c.Validate();err!=nil {t.Fatal(err)}
    text:=strings.Join(EngineArgs(c)," ")
    if !strings.Contains(text,"--filter-tcp=80,443,5222 --filter-l7=mtproto --payload=mtproto_initial --lua-desync=fake:blob=0x00000000:repeats=3") {
        t.Fatal("MTProto signature-specific strategy absent")
    }
    if strings.Contains(text,"--filter-udp=5222") {
        t.Fatal("UDP strategy was enabled unexpectedly")
    }
    found:=false
    for _,r:=range FirewallRules(c) {
        joined:=strings.Join(r.Args," ")
        if strings.Contains(joined,"-p tcp") && strings.Contains(joined,"5222") {found=true}
        if strings.Contains(joined,"-p udp") && strings.Contains(joined,"5222") {t.Fatal("native UDP unnecessarily intercepted")}
    }
    if !found {t.Fatal("TCP/5222 missing from NFQUEUE scope")}
    c.TelegramMTProto=false
    if strings.Contains(strings.Join(EngineArgs(c)," "),"--filter-l7=mtproto") {
        t.Fatal("MTProto profile remained after disabling")
    }
}

func TestTelegramPresetProvidesWebAndAPIWithoutOverlappingOthers(t *testing.T) {
    found:=false
    for _,p:=range ServicePresets() {
        if p.Profile.ID=="telegram-web" {
            found=true
            for _,d:=range []string{"telegram.org","t.me","telegram.dog","tdesktop.com"} {
                ok:=false
                for _,have:=range p.Profile.Domains {if have==d {ok=true}}
                if !ok {t.Fatal("missing Telegram HTTPS site",d)}
            }
        }
    }
    if !found {t.Fatal("Telegram Web missing")}
}
