package pilot

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
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
	Version    string     `json:"version"`
	Mode       string     `json:"mode"`
	Running    bool       `json:"running"`
	Busy       bool       `json:"busy"`
	Config     Config     `json:"config"`
	Job        *Job       `json:"job"`
	Error      string     `json:"error,omitempty"`
	Strategies []Strategy `json:"strategies"`
}
type Manager struct {
	mu        sync.Mutex
	op        sync.Mutex
	c         Config
	path      string
	backend   Backend
	prober    Prober
	log       *LogBuffer
	job       *Job
	lastError string
	cancel    context.CancelFunc
	closed    bool
}

func NewManager(c Config, path string, b Backend, p Prober, log *LogBuffer) *Manager {
	return &Manager{c: c, path: path, backend: b, prober: p, log: log}
}

func cloneConfig(c Config) Config {
	c.Exclusions = append([]string{}, c.Exclusions...)
	c.Profiles = append([]Profile{}, c.Profiles...)
	for i := range c.Profiles {
		c.Profiles[i].Domains = append([]string{}, c.Profiles[i].Domains...)
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
	return Snapshot{Version: Version, Mode: m.backend.Kind(), Running: m.backend.Running(), Busy: busy, Config: cloneConfig(m.c), Job: job, Error: m.lastError, Strategies: Strategies}
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
	old := m.Config()
	if c.Listen != old.Listen || c.EngineDir != old.EngineDir || c.Queue != old.Queue {
		return errors.New("listen, engine_dir и queue меняются в config.json с перезапуском службы")
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
	defer func() {
		if !committed {
			err := m.backend.Start(old)
			m.finish("", errors.Join(errors.New("рабочий вариант не выбран; восстановлена предыдущая конфигурация"), ctx.Err(), err))
		}
	}()
	baseline := m.prober.Check(ctx, p, old, true)
	directOK := allPassed(baseline, p, old)
	m.record(Trial{Strategy: "direct", Passed: directOK, Results: baseline})
	selected := ""
	if directOK {
		selected = "none"
	}
	for _, s := range Strategies {
		if selected != "" || ctx.Err() != nil {
			break
		}
		if s.ID == "none" {
			continue
		}
		m.stage(s.ID)
		candidate := cloneConfig(old)
		candidate.Profiles[idx].Strategy = s.ID
		if err := m.backend.Start(candidate); err != nil {
			m.record(Trial{Strategy: s.ID, Error: err.Error(), Results: []ProbeResult{}})
			continue
		}
		results := m.prober.Check(ctx, p, candidate, false)
		passed := allPassed(results, p, candidate)
		m.record(Trial{Strategy: s.ID, Passed: passed, Results: results})
		if passed {
			selected = s.ID
		}
	}
	if selected == "" || ctx.Err() != nil {
		return
	}
	candidate := cloneConfig(old)
	candidate.Profiles[idx].Strategy = selected
	// Reapply and recheck after selection: the saved result must survive an
	// engine restart, not merely a lucky request from the previous process.
	m.stage("confirm")
	if err := m.backend.Start(candidate); err != nil {
		m.log.Add(err.Error())
		return
	}
	confirmation := m.prober.Check(ctx, p, candidate, false)
	passed := allPassed(confirmation, p, candidate)
	m.record(Trial{Strategy: "confirm:" + selected, Passed: passed, Results: confirmation})
	if !passed || ctx.Err() != nil {
		return
	}
	if err := WriteJSON(m.path+".previous", old); err != nil {
		m.log.Add(err.Error())
		return
	}
	if err := m.save(candidate); err != nil {
		m.log.Add(err.Error())
		return
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
			return m.prober.Check(ctx, p, c, false), nil
		}
	}
	return nil, errors.New("профиль не найден")
}

func (m *Manager) Watch(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	next := time.Now()
	failures := map[string]int{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !m.op.TryLock() {
				continue
			}
			c := m.Config()
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
			if !c.Auto || time.Now().Before(next) {
				m.op.Unlock()
				continue
			}
			next = time.Now().Add(time.Duration(c.IntervalMinutes) * time.Minute)
			toTune := ""
			for _, p := range c.Profiles {
				r := m.prober.Check(ctx, p, c, false)
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
			m.op.Unlock()
			if toTune != "" {
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
	m.op.Lock()
	defer m.op.Unlock()
	return m.backend.Stop()
}
