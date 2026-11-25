package pilot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/iotest"
	"time"
)

func TestStreamingProbeValidatesTheWholeBoundedResponse(t *testing.T) {
	const marker = "метка"
	target := Target{Status: 200, Contains: marker}
	input := strings.Repeat("x", (16<<10)-2) + marker
	for _, reader := range []io.Reader{strings.NewReader(input), iotest.OneByteReader(strings.NewReader(marker))} {
		if err := checkResponseBody(reader, target); err != nil {
			t.Fatal("marker crossed read boundary", err)
		}
	}
	late := errors.New("late failure")
	if err := checkResponseBody(io.MultiReader(strings.NewReader(marker), iotest.ErrReader(late)), target); !errors.Is(err, late) {
		t.Fatal("partially received body was accepted", err)
	}
	if err := checkResponseBody(strings.NewReader(strings.Repeat("x", (2<<20)-1)+marker), target); err == nil {
		t.Fatal("marker past the read limit was accepted")
	}
	if err := checkResponseBody(strings.NewReader(""), Target{Status: 204}); err != nil {
		t.Fatal(err)
	}
	if err := checkResponseBody(strings.NewReader(""), Target{Status: 200}); err == nil {
		t.Fatal("empty response accepted")
	}
}

func TestHealthReportExportsMeasurementsAndPauseReasons(t *testing.T) {
	c := DefaultConfig()
	c.Enabled = true
	c.WAN = "ppp0"
	c.RuntimeDir = t.TempDir()
	m := NewManager(c, filepath.Join(t.TempDir(), "config.json"), &testBackend{running: true}, successfulProber{}, &LogBuffer{})
	if _, err := m.Check(context.Background(), "youtube"); err != nil {
		t.Fatal(err)
	}
	check := m.checks["youtube"]
	check.Results[0].URL = "https://example.com/?token=private-result"
	check.Results[0].Error = "private-error"
	m.checks["youtube"] = check
	m.recordVideo(VideoProbeResult{OK: true, Status: 206, Bytes: 65536, DurationMS: 61, MediaType: "video/private-type", Error: "private-video"})
	for _, tc := range []struct {
		paused     bool
		hold, want string
	}{
		{false, holdCPU, "cpu"}, {false, holdLoad, "load"}, {false, holdMemory, "memory"}, {true, holdCPU, "user"}, {false, holdByUser, ""},
	} {
		m.c.BackgroundPaused, m.backgroundHold = tc.paused, tc.hold
		report := m.Diagnostics()
		summary := report.Profiles[0].Check
		if report.Resources.BackgroundHold != tc.want || report.Settings.BackgroundPaused != tc.paused || summary == nil ||
			!summary.Passed || !summary.Complete || summary.Stale || summary.Successful != len(check.Results) || report.Profiles[1].Check != nil ||
			report.LastVideo == nil || !report.LastVideo.OK || report.LastVideo.Bytes != 65536 || report.LastVideo.Finished.IsZero() {
			t.Fatalf("missing diagnostics: hold=%q, check=%+v, video=%+v", report.Resources.BackgroundHold, summary, report.LastVideo)
		}
		raw, err := json.Marshal(report)
		if err != nil || bytes.Contains(raw, []byte("private-")) {
			t.Fatal("unexpected free text in diagnostic export", err)
		}
		report.LastVideo.Status = 500
		if m.Snapshot().LastVideo.Status != 206 {
			t.Fatal("reader changed stored measurement")
		}
	}
}

func TestManualCheckCooldownReportsRetryAfter(t *testing.T) {
	c := DefaultConfig()
	m := NewManager(c, filepath.Join(t.TempDir(), "config.json"), &testBackend{}, successfulProber{}, &LogBuffer{})
	m.checks = map[string]ServiceCheck{"youtube": {Finished: time.Now()}}
	token := strings.Repeat("k", 40)
	req := httptest.NewRequest("POST", "/api/check", strings.NewReader(`{"profile":"youtube"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	Handler(m, token).ServeHTTP(rec, req)
	seconds, err := strconv.Atoi(rec.Header().Get("Retry-After"))
	if rec.Code != http.StatusTooManyRequests || err != nil || seconds <= 0 || seconds > 20 {
		t.Fatalf("invalid retry response: %d, %d", rec.Code, seconds)
	}
}

func BenchmarkProbeBody2MiB(b *testing.B) {
	// Put the marker at the end to exercise the full scan.
	t := Target{Status: 200, Contains: "pilot-marker"}
	body := []byte(strings.Repeat("x", (2<<20)-len(t.Contains)) + t.Contains)
	b.SetBytes(int64(len(body)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := checkResponseBody(bytes.NewReader(body), t); err != nil {
			b.Fatal(err)
		}
	}
}
