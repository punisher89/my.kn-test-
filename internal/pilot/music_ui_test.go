package pilot

import (
  "net/http/httptest"
  "path/filepath"
  "strings"
  "testing"
)

func TestBrowserMusicPlayerEmbeddedWithNarrowMediaCSP(t *testing.T) {
  m:=NewManager(DefaultConfig(),filepath.Join(t.TempDir(),"config.json"),&testBackend{},successfulProber{},&LogBuffer{})
  h:=Handler(m,strings.Repeat("a",64))
  for _,target:=range []struct{path,expected string}{
    {"/","id=\"pilot-music\""},
    {"/music.js","createObjectURL"},
    {"/style.css",".music-popover"},
  } {
    w:=httptest.NewRecorder()
    h.ServeHTTP(w,httptest.NewRequest("GET","http://pilot.test"+target.path,nil))
    if w.Code!=200 {t.Fatalf("%s not served: HTTP %d",target.path,w.Code)}
    if !strings.Contains(w.Body.String(),target.expected) {t.Fatalf("%s asset missing %q",target.path,target.expected)}
    csp:=w.Header().Get("Content-Security-Policy")
    for _,rule:=range []string{"default-src 'self'","media-src 'self' blob: https:","script-src 'self'","connect-src 'self'","frame-ancestors 'none'"} {
      if !strings.Contains(csp,rule) {t.Fatalf("missing CSP rule %q on %s: %s",rule,target.path,csp)}
    }
    if strings.Contains(csp,"'unsafe-inline'") || strings.Contains(csp,"http:") {t.Fatalf("music weakened CSP on %s: %s",target.path,csp)}
  }
}
