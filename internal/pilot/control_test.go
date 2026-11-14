package pilot

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPrivateControlSocketAndSingleInstance(t *testing.T) {
	dir, err := os.MkdirTemp("", "kp-control-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	lock, err := LockInstance(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if other, err := LockInstance(dir); err == nil {
		other.Close()
		t.Fatal("second daemon acquired lock")
	}
	l, err := ControlListener(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	info, err := os.Stat(filepath.Join(dir, "control.sock"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatal("control socket not private")
	}
	m := NewManager(DefaultConfig(), filepath.Join(dir, "config.json"), Development{}, HTTPProber{}, &LogBuffer{})
	server := &http.Server{Handler: ControlHandler(m)}
	defer server.Close()
	go server.Serve(l)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	data, err := ControlRequest(ctx, dir, "GET", "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	var snapshot Snapshot
	if err = json.Unmarshal(data, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Mode != "development" || snapshot.Running {
		t.Fatal("health misreported mode")
	}
	if _, err = ControlRequest(ctx, dir, "POST", "/repair"); err != nil {
		t.Fatal(err)
	}
	if _, err = ControlRequest(ctx, dir, "GET", "/repair"); err == nil {
		t.Fatal("unsafe method accepted")
	}
}

func successfulResults(p Profile, c Config, direct bool) []ProbeResult {
	r := []ProbeResult{}
	families := []string{"tcp4"}
	if c.IPv6 {
		families = append(families, "tcp6")
	}
	for _, target := range p.Targets {
		for _, family := range families {
			for _, version := range []string{"1.2", "1.3"} {
				for attempt := 1; attempt <= c.Attempts; attempt++ {
					r = append(r, ProbeResult{URL: target.URL, TLS: version, Family: family, Attempt: attempt, Direct: direct, OK: true, Status: target.Status})
				}
			}
		}
	}
	return r
}

type successfulProber struct{}

func (successfulProber) Check(_ context.Context, p Profile, c Config, direct bool) []ProbeResult {
	return successfulResults(p, c, direct)
}

func TestPassingDirectProbeIsConfirmedAndPersisted(t *testing.T) {
	c := DefaultConfig()
	c.Enabled = true
	c.WAN = "ppp0"
	c.IPv6 = true
	path := filepath.Join(t.TempDir(), "config.json")
	if err := WriteJSON(path, c); err != nil {
		t.Fatal(err)
	}
	m := NewManager(c, path, &testBackend{running: true}, successfulProber{}, &LogBuffer{})
	if err := m.BeginTune("youtube"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		s := m.Snapshot()
		if s.Job.Finished != nil && !s.Busy {
			break
		}
		time.Sleep(time.Millisecond)
	}
	s := m.Snapshot()
	if s.Job.Finished == nil || s.Job.Selected != "none" || s.Job.Stage != "done" {
		t.Fatalf("bad job: %+v", s.Job)
	}
	saved, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Profiles[0].Strategy != "none" {
		t.Fatal("not persisted")
	}
	if len(s.Job.Trials) != 2 || s.Job.Trials[1].Strategy != "confirm:none" {
		t.Fatal("restart confirmation missing")
	}
	restored := NewManager(saved, path, Development{}, HTTPProber{}, &LogBuffer{}).Snapshot()
	if restored.Job == nil || restored.Job.Selected != "none" {
		t.Fatal("last report not restored after restart")
	}
}

func TestMissingTLS12AndDuplicateResultsCannotPass(t *testing.T) {
	c := DefaultConfig()
	p := c.Profiles[0]
	results := successfulResults(p, c, false)
	if !allPassed(results, p, c) {
		t.Fatal("valid results failed")
	}
	for i := range results {
		results[i].TLS = "1.3"
	}
	if allPassed(results, p, c) {
		t.Fatal("duplicate TLS 1.3 entries masqueraded as TLS 1.2 coverage")
	}
}
