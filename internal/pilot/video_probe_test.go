package pilot

import (
    "bytes"
    "context"
    "encoding/json"
    "io"
    "net/http"
    "net/http/httptest"
    "path/filepath"
    "strings"
    "testing"
    "time"
    "fmt"
)

func TestVideoURLIsScopedAndTemporary(t *testing.T) {
    now := time.Now()
    expires := now.Add(time.Hour).Unix()
    good := fmt.Sprintf("https://rr1---sn-ab.googlevideo.com/videoplayback?expire=%d&sig=PRIVATE_SIGNATURE", expires)
    if _, err := parseVideoURL(good, now); err != nil { t.Fatal(err) }
    for _, raw := range []string{
        fmt.Sprintf("https://rr1.googlevideo.com.evil.test/videoplayback?expire=%d&sig=x",expires),
        fmt.Sprintf("https://localhost/videoplayback?expire=%d&sig=x",expires),
        fmt.Sprintf("http://rr1.googlevideo.com/videoplayback?expire=%d&sig=x",expires),
        fmt.Sprintf("https://rr1.googlevideo.com:444/videoplayback?expire=%d&sig=x",expires),
        fmt.Sprintf("https://name:pass@rr1.googlevideo.com/videoplayback?expire=%d&sig=x",expires),
        fmt.Sprintf("https://rr1.googlevideo.com/videoplayback-preview?expire=%d&sig=x",expires),
        fmt.Sprintf("https://rr1.googlevideo.com/other?expire=%d&sig=x",expires),
        fmt.Sprintf("https://rr1.googlevideo.com/videoplayback?expire=%d&sig=x",now.Add(-time.Hour).Unix()),
        fmt.Sprintf("https://rr1.googlevideo.com/videoplayback?expire=%d&sig=x",now.Add(48*time.Hour).Unix()),
        "https://rr1.googlevideo.com/videoplayback?sig=NO_EXPIRY",
    } {
        if _, err := parseVideoURL(raw, now); err == nil { t.Errorf("accepted unexpected URL category: %q", strings.Split(raw, "?")[0]) }
    }
}

func videoResponse(status int, contentType, contentRange string, body []byte, length int64) *http.Response {
    return &http.Response{
        StatusCode: status, Header: http.Header{
            "Content-Type": []string{contentType},
            "Content-Range": []string{contentRange},
        },
        Body: io.NopCloser(bytes.NewReader(body)), ContentLength: length,
    }
}

func TestVideoFragmentRequiresRealCompletePartialVideo(t *testing.T) {
    body := bytes.Repeat([]byte{0x11}, videoProbeLimit)
    cases := []struct {
        name string
        response *http.Response
        want bool
    }{
        {"good video",videoResponse(206,"video/mp4","bytes 0-65535/999999",body,int64(len(body))),true},
        {"200 HTML",videoResponse(200,"text/html","",body,int64(len(body))),false},
        {"206 HTML",videoResponse(206,"text/html","bytes 0-65535/999999",body,int64(len(body))),false},
        {"206 audio",videoResponse(206,"audio/mp4","bytes 0-65535/999999",body,int64(len(body))),false},
        {"no range",videoResponse(206,"video/mp4","",body,int64(len(body))),false},
        {"wrong start",videoResponse(206,"video/mp4","bytes 10-65535/999999",body,int64(len(body))),false},
        {"truncated body",videoResponse(206,"video/mp4","bytes 0-65535/999999",body[:1024],int64(len(body))),false},
        {"wrong content length",videoResponse(206,"video/mp4","bytes 0-65535/999999",body,1024),false},
        {"extra bytes",videoResponse(206,"video/mp4","bytes 0-65535/999999",append(append([]byte{},body...),0x11),-1),false},
        {"short valid clip",videoResponse(206,"video/webm","bytes 0-1023/1024",body[:1024],1024),true},
    }
    for _, tc := range cases {
        t.Run(tc.name,func(t *testing.T) {
            r:=readVideoFragment(context.Background(),tc.response,time.Now(),VideoProbeResult{})
            if r.OK != tc.want { t.Fatalf("got %+v; want ok=%t",r,tc.want) }
            if tc.want && r.Bytes != int(tc.response.ContentLength) && tc.response.ContentLength>=0 {
                t.Fatalf("bad bytes: %+v",r)
            }
        })
    }
}

func TestVideoProbeAPIAuthAndNeverEchoesSignedURL(t *testing.T) {
    m := NewManager(DefaultConfig(),filepath.Join(t.TempDir(),"config.json"),&testBackend{},successfulProber{},&LogBuffer{})
    token := strings.Repeat("k", 40)
    h := Handler(m,token)
    signed := fmt.Sprintf("https://rr1.googlevideo.com/videoplayback?expire=%d&sig=TOP_SECRET_TOKEN",time.Now().Add(-time.Hour).Unix())
    payload,_:=json.Marshal(map[string]string{"url":signed})
    makeReq := func(authorized bool) *httptest.ResponseRecorder {
        req:=httptest.NewRequest("POST","http://pilot.test/api/youtube/video-test",bytes.NewReader(payload))
        req.Header.Set("Content-Type","application/json")
        if authorized { req.Header.Set("Authorization","Bearer "+token) }
        rec:=httptest.NewRecorder()
        h.ServeHTTP(rec,req)
        return rec
    }
    if r:=makeReq(false);r.Code!=401 { t.Fatalf("missing auth: %d",r.Code) }
    r:=makeReq(true)
    if r.Code!=400 { t.Fatalf("expected invalid expired URL, got %d",r.Code) }
    if strings.Contains(r.Body.String(),"TOP_SECRET_TOKEN") || strings.Contains(r.Body.String(),"expire=") {
        t.Fatal("signed URL leaked in API response")
    }
    if m.Snapshot().Checks["youtube"].Passed { t.Fatal("URL validation incorrectly marked video as verified") }
}

func TestCDNWithoutSignedVideoEvidenceRetainsWorkingStrategy(t *testing.T) {
    m, cfg, _, scope := activeFixture(t)
    scope.Host = "rr1---sn-ab.googlevideo.com"
    m.domainJob.Scope = scope
    calls := 0
    m.activeProbeHook = func(_ context.Context, _ Config, _ DomainScope, _ Target, _ string) activeEvidence {
        calls++
        return evidence(true, true)
    }
    original := cfg.Profiles[0].Strategy
    stage, _ := m.searchDomain(context.Background(),cfg,scope)
    if stage != "unmeasured" || m.domainJob.Reason != "video_url_required" {
        t.Fatalf("CDN generic HTTP was mistaken for a video: %+v", m.domainJob)
    }
    if calls != 0 || len(m.domainPlans.Plans) != 0 || cfg.Profiles[0].Strategy != original {
        t.Fatal("unverified CDN response changed the working method")
    }
}
