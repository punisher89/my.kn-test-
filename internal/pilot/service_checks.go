package pilot

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"
)

// A bounded, read-only check of configured targets, never an arbitrary scan of
// imported domains. Only the latest report per existing profile stays in RAM.
type ServiceCheck struct {
	Profile     string        `json:"profile"`
	Started     time.Time     `json:"started"`
	Finished    time.Time     `json:"finished"`
	Results     []ProbeResult `json:"results"`
	Passed      bool          `json:"passed"`
	Complete    bool          `json:"complete"`
	Running     bool          `json:"running"`
	Strategy    string        `json:"strategy"`
	Adaptive    bool          `json:"adaptive"`
	Stale       bool          `json:"stale"`
	Error       string        `json:"error,omitempty"`
	fingerprint [32]byte
}

func checkFingerprint(c Config, p Profile) [32]byte {
	// Source timestamps and names do not change how a target is checked.
	p.Name = ""
	p.DomainSource = ""
	p.AutoDomains = false
	p.DomainSHA256 = ""
	p.SourceExclusions = nil
	p.DomainsUpdated = time.Time{}
	p.DomainRevision = 0
	b, _ := json.Marshal(struct {
		Profile                 Profile
		WAN                     string
		Enabled, IPv6, Adaptive bool
		Timeout, Attempts       int
		Pins                    []DomainPin
		Paused, Exclusions      []string
	}{p, c.WAN, c.Enabled, c.IPv6, c.Adaptive, c.TimeoutSeconds, c.Attempts, append([]DomainPin{}, c.DomainPins...), append([]string{}, c.PausedDomains...), append([]string{}, c.Exclusions...)})
	return sha256.Sum256(b)
}

func (m *Manager) checkLocked(ctx context.Context, p Profile, c Config) []ProbeResult {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	report := ServiceCheck{Profile: p.ID, Started: time.Now(), Running: m.backend.Running(), Strategy: p.Strategy, Adaptive: c.Adaptive, fingerprint: checkFingerprint(c, p)}
	c.probeRetryFailure = true
	report.Results = m.prober.Check(ctx, p, c, false)
	if report.Results == nil {
		report.Results = []ProbeResult{}
	}
	report.Finished = time.Now()
	want := len(p.Targets) * 2 * c.Attempts
	if c.IPv6 {
		want *= 2
	}
	report.Complete = len(report.Results) == want && ctx.Err() == nil
	report.Passed = report.Complete && allPassed(report.Results, p, c)
	if ctx.Err() != nil {
		report.Error = "Проверка прервана или достигла общего лимита 90 секунд"
	}
	m.mu.Lock()
	if m.checks == nil {
		m.checks = map[string]ServiceCheck{}
	}
	// Prune profiles removed since a previous report to bound memory.
	for id := range m.checks {
		found := false
		for _, profile := range c.Profiles {
			if profile.ID == id {
				found = true
				break
			}
		}
		if !found {
			delete(m.checks, id)
		}
	}
	m.checks[p.ID] = report
	m.mu.Unlock()
	return append([]ProbeResult{}, report.Results...)
}

// Manual HTTPS results are observations, not a permanent health guarantee.
const serviceCheckFreshness = 15 * time.Minute

func (m *Manager) checksLocked(running bool) map[string]ServiceCheck {
	now := time.Now()
	reports := map[string]ServiceCheck{}
	for _, p := range m.c.Profiles {
		if r, ok := m.checks[p.ID]; ok {
			r.Results = append([]ProbeResult{}, r.Results...)
			r.Stale = r.Finished.IsZero() || now.Sub(r.Finished) >= serviceCheckFreshness || r.fingerprint != checkFingerprint(m.c, p) || r.Running != running
			reports[p.ID] = r
		}
	}
	return reports
}

const (
	holdCPU    = "CPU занят на 65% или больше; фоновые проверки временно отложены"
	holdLoad   = "высокая очередь задач CPU"
	holdMemory = "мало доступной памяти"
	holdByUser = "фоновые проверки приостановлены пользователем"
)

// Unknown sensors are not treated as measured low load. Known pressure defers
// background work, never stops the working bypass or changes its strategy.
func backgroundPressure(d DeviceInfo) string {
	if d.MemoryAvailable != nil && (*d.MemoryAvailable < 16<<20 || (d.MemoryTotal > 0 && float64(*d.MemoryAvailable)/float64(d.MemoryTotal) < 0.08)) {
		return holdMemory
	}
	if d.CPUPercent != nil && *d.CPUPercent >= 65 {
		return holdCPU
	}
	if len(d.Load) > 0 && d.CPUCores > 0 && d.Load[0] >= float64(d.CPUCores) {
		return holdLoad
	}
	return ""
}
func (m *Manager) backgroundAllowed(c Config) bool {
	reason := ""
	if c.BackgroundPaused {
		m.mu.Lock()
		m.backgroundHold = holdByUser
		m.mu.Unlock()
		return false
	}
	if c.ProbeLoadGuard {
		reason = backgroundPressure(m.guardDevice.ReadResources("", time.Now()))
	}
	m.mu.Lock()
	m.backgroundHold = reason
	m.mu.Unlock()
	return reason == ""
}

type checkRetryAfter int

func (seconds checkRetryAfter) Error() string {
	return fmt.Sprintf("повторная проверка этого сервиса доступна через %d с", seconds)
}

func checkCooldown(r ServiceCheck) error {
	if left := 20*time.Second - time.Since(r.Finished); !r.Finished.IsZero() && left > 0 {
		return checkRetryAfter((left + time.Second - 1) / time.Second)
	}
	return nil
}
