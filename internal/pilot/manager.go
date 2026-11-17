package pilot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

var ErrBusy = errors.New("выполняется другая операция; дождитесь её завершения или отмените подбор")

type Trial struct {
	Strategy string        `json:"strategy"`
	Passed   bool          `json:"passed"`
	Results  []ProbeResult `json:"results"`
	Error    string        `json:"error,omitempty"`
}
type Job struct {
	Profile  string     `json:"profile"`
	Stage    string     `json:"stage"`
	Started  time.Time  `json:"started"`
	Finished *time.Time `json:"finished,omitempty"`
	Selected string     `json:"selected,omitempty"`
	Error    string     `json:"error,omitempty"`
	Trials   []Trial    `json:"trials"`
}
type Snapshot struct {
	DomainJob       *DomainSearch           `json:"domain_job,omitempty"`
	DomainQueue     int                     `json:"domain_queue"`
	VerifiedDomains int                     `json:"verified_domains"`
	Checks          map[string]ServiceCheck `json:"checks"`
	BackgroundHold  string                  `json:"background_hold,omitempty"`
	ServicePresets  []ServicePreset         `json:"service_presets"`
	Version         string                  `json:"version"`
	Mode            string                  `json:"mode"`
	Running         bool                    `json:"running"`
	Busy            bool                    `json:"busy"`
	Config          Config                  `json:"config"`
	Job             *Job                    `json:"job"`
	Error           string                  `json:"error,omitempty"`
	Strategies      []Strategy              `json:"strategies"`
}
type Manager struct {
	domainJob        *DomainSearch
	domainQueue      int
	domainTasks      map[DomainScope]*domainTask
	domainPlans      domainPlanStore
	domainPlanIssue  string
	domainPublished  string
	activeProbeHook  activeProbeFunc
	activeHello      *helloSample
	checks           map[string]ServiceCheck
	lastVideoProbeAt time.Time
	backgroundHold   string
	guardDevice      deviceReader
	domainCacheError error
	updating         atomic.Bool
	updateRequested  atomic.Bool
	updateChecking   atomic.Bool
	release          *releaseDocument
	sourceReleases   map[string]sourceReleaseDocument
	domainCache      *domainRegistry
	domainCacheAt    time.Time
	device           deviceReader
	radio            radioReader
	mu               sync.Mutex
	op               sync.Mutex
	c                Config
	path             string
	backend          Backend
	prober           Prober
	log              *LogBuffer
	job              *Job
	lastError        string
	cancel           context.CancelFunc
	closed           bool
}

func NewManager(c Config, path string, b Backend, p Prober, log *LogBuffer) *Manager {
	m := &Manager{c: c, path: path, backend: b, prober: p, log: log}
	if data, err := os.ReadFile(filepath.Join(filepath.Dir(path), "last-tune.json")); err == nil {
		var job Job
		if json.Unmarshal(data, &job) == nil && job.Finished != nil {
			m.job = &job
		}
	}
	m.loadDomainPlans()
	if data, err := os.ReadFile(filepath.Join(filepath.Dir(path), "last-domain-search.json")); err == nil && len(data) < 1<<20 {
		var job DomainSearch
		if json.Unmarshal(data, &job) == nil && job.Finished != nil && job.Scope.valid() && len(job.Trials) <= 128 {
			m.domainJob = &job
		}
	}
	return m
}

func cloneConfig(c Config) Config {
	c.Catalog = append([]Strategy{}, c.Catalog...)
	for i := range c.Catalog {
		c.Catalog[i].Actions = append([]string{}, c.Catalog[i].Actions...)
	}

	c.DomainPins = append([]DomainPin{}, c.DomainPins...)
	c.PausedDomains = append([]string{}, c.PausedDomains...)
	c.Exclusions = append([]string{}, c.Exclusions...)
	c.Profiles = append([]Profile{}, c.Profiles...)
	for i := range c.Profiles {
		c.Profiles[i].Domains = append([]string{}, c.Profiles[i].Domains...)
		c.Profiles[i].SourceExclusions = append([]string{}, c.Profiles[i].SourceExclusions...)
		c.Profiles[i].Targets = append([]Target{}, c.Profiles[i].Targets...)
	}
	return c
}
func (m *Manager) Config() Config { m.mu.Lock(); defer m.mu.Unlock(); return cloneConfig(m.c) }
func (m *Manager) Snapshot() Snapshot {
	busy := !m.op.TryLock()
	if !busy {
		m.op.Unlock()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var job *Job
	if m.job != nil {
		j := *m.job
		j.Trials = append([]Trial{}, m.job.Trials...)
		job = &j
	}
	var domainJob *DomainSearch
	if m.domainJob != nil {
		j := *m.domainJob
		j.Trials = append([]Trial{}, m.domainJob.Trials...)
		for i := range j.Trials {
			j.Trials[i].Results = append([]ProbeResult{}, j.Trials[i].Results...)
		}
		domainJob = &j
	}
	return Snapshot{DomainJob: domainJob, DomainQueue: m.domainQueue, VerifiedDomains: len(m.domainPlans.Plans), Checks: m.checksLocked(), BackgroundHold: m.backgroundHold, ServicePresets: ServicePresets(), Version: Version, Mode: m.backend.Kind(), Running: m.backend.Running(), Busy: busy, Config: cloneConfig(m.c), Job: job, Error: m.lastError, Strategies: StrategiesFor(m.c)}
}
func (m *Manager) setError(err error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastError = ""
	if err != nil {
		m.lastError = err.Error()
		m.log.Add(err.Error())
	}
	return err
}
func (m *Manager) save(c Config) error {
	c.tuning = false
	c.probeFailFast = false
	if err := WriteJSON(m.path, c); err != nil {
		return err
	}
	m.mu.Lock()
	m.c = cloneConfig(c)
	m.mu.Unlock()
	return nil
}

func (m *Manager) Change(c Config) error {
	if err := c.Validate(); err != nil {
		return err
	}
	if !m.op.TryLock() {
		return ErrBusy
	}
	defer m.op.Unlock()
	return m.changeLocked(c)
}

func (m *Manager) changeLocked(c Config) error {
	old := m.Config()
	for i := range c.Profiles {
		for _, p := range old.Profiles {
			if p.ID == c.Profiles[i].ID && p.DomainSource != c.Profiles[i].DomainSource {
				c.Profiles[i].DomainRevision = 0
				c.Profiles[i].DomainsUpdated = time.Time{}
			}
		}
	}
	if c.RuntimeDir != old.RuntimeDir || c.Listen != old.Listen || c.EngineDir != old.EngineDir || c.Queue != old.Queue {
		return errors.New("listen, engine_dir, runtime_dir и queue меняются в config.json с перезапуском службы")
	}
	c.Catalog = old.Catalog
	c.CatalogRevision = old.CatalogRevision
	if err := c.Validate(); err != nil {
		return err
	}
	c.Enabled = old.Enabled
	if c.Enabled {
		if err := m.backend.Start(c); err != nil {
			return m.setError(errors.Join(err, m.backend.Start(old)))
		}
	}
	if err := WriteJSON(m.path+".previous", old); err != nil {
		if old.Enabled {
			_ = m.backend.Start(old)
		}
		return m.setError(err)
	}
	if err := m.save(c); err != nil {
		if old.Enabled {
			err = errors.Join(err, m.backend.Start(old))
		}
		return m.setError(err)
	}
	return m.setError(nil)
}
func (m *Manager) SetEnabled(enabled bool) error {
	if !m.op.TryLock() {
		return ErrBusy
	}
	defer m.op.Unlock()
	c := m.Config()
	old := c
	var err error
	if enabled {
		err = m.backend.Start(c)
	} else {
		err = m.backend.Stop()
	}
	if err != nil {
		return m.setError(err)
	}
	c.Enabled = enabled
	if err = m.save(c); err != nil {
		if old.Enabled {
			err = errors.Join(err, m.backend.Start(old))
		} else {
			err = errors.Join(err, m.backend.Stop())
		}
		return m.setError(err)
	}
	return m.setError(nil)
}
func (m *Manager) Boot() {
	if m.Config().Enabled {
		if err := m.backend.Start(m.Config()); err != nil {
			m.setError(err)
		}
	} else {
		if err := m.backend.Stop(); err != nil {
			m.setError(err)
		}
	}
}

func (m *Manager) BeginTune(id string) error {
	if !m.op.TryLock() {
		return ErrBusy
	}
	c := m.Config()
	idx := -1
	for i, p := range c.Profiles {
		if p.ID == id {
			idx = i
		}
	}
	if idx >= 0 {
		if reason := profileControlReason(c, c.Profiles[idx]); reason != "" {
			m.op.Unlock()
			return errors.New(reason)
		}
	}
	if idx < 0 {
		m.op.Unlock()
		return errors.New("профиль не найден")
	}
	if !c.Enabled || !m.backend.Running() {
		m.op.Unlock()
		return errors.New("сначала запустите обход")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		cancel()
		m.op.Unlock()
		return errors.New("служба останавливается")
	}
	m.cancel = cancel
	m.job = &Job{Profile: id, Stage: "baseline", Started: time.Now().UTC(), Trials: []Trial{}}
	m.mu.Unlock()
	go func() {
		defer m.op.Unlock()
		defer cancel()
		m.tune(ctx, c, idx)
		m.mu.Lock()
		m.cancel = nil
		m.mu.Unlock()
	}()
	return nil
}
func (m *Manager) CancelTune() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cancel != nil {
		m.cancel()
	}
}
func (m *Manager) stage(stage string) { m.mu.Lock(); m.job.Stage = stage; m.mu.Unlock() }
func (m *Manager) record(t Trial)     { m.mu.Lock(); m.job.Trials = append(m.job.Trials, t); m.mu.Unlock() }
func (m *Manager) finish(selected string, err error) {
	m.mu.Lock()
	now := time.Now().UTC()
	m.job.Finished = &now
	m.job.Selected = selected
	m.job.Stage = "done"
	if err != nil {
		m.job.Stage = "failed"
		m.job.Error = err.Error()
	}
	job := *m.job
	m.mu.Unlock()
	if e := WriteJSON(filepath.Join(filepath.Dir(m.path), "last-tune.json"), job); e != nil {
		m.log.Add("Не сохранён отчёт: " + e.Error())
	}
	if err != nil {
		m.setError(err)
	} else {
		m.setError(nil)
		m.log.Add("Подбор завершён: " + job.Profile + " → " + selected)
	}
}

func (m *Manager) tune(ctx context.Context, old Config, idx int) {
	p := old.Profiles[idx]
	committed := false
	var cause error
	defer func() {
		if !committed {
			err := m.backend.Start(old)
			message := "рабочий вариант не выбран; предыдущая конфигурация восстановлена"
			if err != nil {
				message = "рабочий вариант не выбран; восстановить предыдущую конфигурацию не удалось"
			}
			m.finish("", errors.Join(errors.New(message), cause, ctx.Err(), err))
		}
	}()
	// Screening visits every target/TLS/IP once. Confirmation always uses the
	// user's original attempt count and timeout after a fresh backend restart.
	selected := ""
	var candidate Config
	confirm := func(id string) bool {
		candidate = cloneConfig(old)
		candidate.Profiles[idx].Strategy = id
		candidate.tuning = true
		candidate.probeFailFast = true
		m.stage("confirm")
		if err := m.backend.Start(candidate); err != nil {
			m.record(Trial{Strategy: "confirm:" + id, Error: err.Error(), Results: []ProbeResult{}})
			return false
		}
		results := m.prober.Check(ctx, p, candidate, false)
		passed := allPassed(results, p, candidate) && ctx.Err() == nil
		m.record(Trial{Strategy: "confirm:" + id, Passed: passed, Results: results})
		return passed
	}
	directConfig := cloneConfig(old)
	directConfig.Attempts = 1
	directConfig.probeFailFast = true
	baseline := m.prober.Check(ctx, p, directConfig, true)
	directOK := allPassed(baseline, p, directConfig) && ctx.Err() == nil
	m.record(Trial{Strategy: "direct", Passed: directOK, Results: baseline})
	if directOK && confirm("none") {
		selected = "none"
	}
	for _, s := range tuningStrategies(old, p, ReadAdaptive(old), time.Now().Unix()) {
		if selected != "" || ctx.Err() != nil {
			break
		}
		if s.ID == "none" {
			continue
		}
		m.stage(s.ID)
		trial := cloneConfig(old)
		trial.Profiles[idx].Strategy = s.ID
		trial.tuning = true
		trial.Attempts = 1
		trial.probeFailFast = true
		if err := m.backend.Start(trial); err != nil {
			m.record(Trial{Strategy: s.ID, Error: err.Error(), Results: []ProbeResult{}})
			continue
		}
		results := m.prober.Check(ctx, p, trial, false)
		passed := allPassed(results, p, trial) && ctx.Err() == nil
		m.record(Trial{Strategy: s.ID, Passed: passed, Results: results})
		if passed && confirm(s.ID) {
			selected = s.ID
		}
	}
	if selected == "" || ctx.Err() != nil {
		return
	}
	if err := WriteJSON(m.path+".previous", old); err != nil {
		cause = err
		return
	}
	if err := m.save(candidate); err != nil {
		cause = err
		return
	}
	candidate.tuning = false
	if candidate.Adaptive {
		if err := m.backend.Start(candidate); err != nil {
			cause = errors.Join(err, m.save(old))
			return
		}
	}
	committed = true
	m.finish(selected, nil)
}

func (m *Manager) Check(ctx context.Context, id string) ([]ProbeResult, error) {
	if !m.op.TryLock() {
		return nil, ErrBusy
	}
	defer m.op.Unlock()
	c := m.Config()
	for _, p := range c.Profiles {
		if p.ID == id {
			m.mu.Lock()
			previous := m.checks[id]
			m.mu.Unlock()
			if err := checkCooldown(previous); err != nil {
				return nil, err
			}
			return m.checkLocked(ctx, p, c), nil
		}
	}
	return nil, errors.New("профиль не найден")
}

func (m *Manager) Watch(ctx context.Context) {
	go m.watchActive(ctx)
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	next := time.Now()
	failures := map[string]int{}
	profileIndex := 0
	nextCatalog := time.Now()
	nextDomains := map[string]time.Time{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if m.updateRequested.Load() {
				continue
			}
			if !m.op.TryLock() {
				continue
			}
			if m.updateRequested.Load() {
				m.op.Unlock()
				continue
			}
			c := m.Config()
			allowed := m.backgroundAllowed(c)
			if allowed && c.AutoCatalog && time.Now().After(nextCatalog) {
				nextCatalog = time.Now().Add(24 * time.Hour)
				if err := m.updateCatalog(ctx); err != nil {
					m.log.Add("Каталог стратегий: " + err.Error())
				}
				c = m.Config()
			}
			for _, p := range c.Profiles {
				if allowed && p.AutoDomains && p.DomainSource != "" && time.Now().After(nextDomains[p.ID]) {
					nextDomains[p.ID] = time.Now().Add(24 * time.Hour)
					if err := m.updateServiceDomains(ctx, p.ID); err != nil {
						m.log.Add("Домены " + p.ID + ": " + err.Error())
					}
				}
			}
			c = m.Config()
			if !c.Enabled {
				m.op.Unlock()
				continue
			}
			var err error
			if !m.backend.Running() {
				err = m.backend.Start(c)
			} else {
				err = m.backend.Repair()
			}
			if err != nil {
				m.setError(err)
				m.op.Unlock()
				continue
			}
			if (c.Adaptive && c.AdaptiveActive) || !c.Auto || !allowed || time.Now().Before(next) {
				m.op.Unlock()
				continue
			}
			next = time.Now().Add(time.Duration(c.IntervalMinutes) * time.Minute)
			toTune := ""
			checkContext, cancelChecks := context.WithTimeout(ctx, 90*time.Second)
			for checked := 0; checked < len(c.Profiles); checked++ {
				if checkContext.Err() != nil {
					break
				}
				if !m.backgroundAllowed(c) {
					next = time.Now().Add(30 * time.Second)
					break
				}
				// Resume from the next profile after a slow/failed one so a
				// bounded cycle cannot permanently starve later services.
				p := c.Profiles[profileIndex%len(c.Profiles)]
				profileIndex = (profileIndex + 1) % len(c.Profiles)
				if profileControlReason(c, p) != "" {
					failures[p.ID] = 0
					continue
				}
				r := m.checkLocked(checkContext, p, c)
				if checkContext.Err() != nil {
					break
				} // A time budget is not a site failure.
				if allPassed(r, p, c) {
					failures[p.ID] = 0
				} else {
					failures[p.ID]++
					if failures[p.ID] >= 2 {
						toTune = p.ID
						break
					}
				}
			}
			cancelChecks()
			m.op.Unlock()
			if toTune != "" && m.backgroundAllowed(c) {
				failures[toTune] = 0
				if err = m.BeginTune(toTune); err != nil {
					m.log.Add(fmt.Sprintf("Автоподбор: %v", err))
				}
			}
		}
	}
}
func (m *Manager) Close() error {
	m.mu.Lock()
	m.closed = true
	if m.cancel != nil {
		m.cancel()
	}
	m.mu.Unlock()
	if m.updating.Load() {
		return m.backend.Stop()
	}
	m.op.Lock()
	defer m.op.Unlock()
	return m.backend.Stop()
}
