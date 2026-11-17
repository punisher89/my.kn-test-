package pilot

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type DomainSearch struct {
	CandidateIndex int         `json:"candidate_index"`
	CandidateTotal int         `json:"candidate_total"`
	Scope          DomainScope `json:"scope"`
	Stage          string      `json:"stage"`
	Started        time.Time   `json:"started"`
	Finished       *time.Time  `json:"finished,omitempty"`
	NextAt         *time.Time  `json:"next_at,omitempty"`
	Candidate      string      `json:"candidate,omitempty"`
	Selected       string      `json:"selected,omitempty"`
	Error          string      `json:"error,omitempty"`
	Reason         string      `json:"reason,omitempty"`
	Trials         []Trial     `json:"trials"`
}
type domainTask struct {
	Cursor          int
	Pool            []string
	Fingerprint     string
	Scope           DomainScope
	Signal, Handled int64
	Next            time.Time
	Attempted       time.Time
	ProofFailures   int
}

// Stable codes are safe to include in exported diagnostics; raw probe errors
// can contain private URLs and must stay in the authenticated local report.
func activeReasonCode(reason string) string {
	switch reason {
	case "resources", "execution_unconfirmed", "http_response", "dns", "certificate", "client_hello", "probe_unmeasured", "client_hello_missing", "mixed_direct", "time_slice", "switch_budget":
		return reason
	}
	return ""
}

func activeProbeReason(r ProbeResult) string {
	if r.Status != 0 {
		return "http_response"
	}
	message := strings.ToLower(r.Error)
	if strings.Contains(message, "dns:") || strings.Contains(message, "no such host") {
		return "dns"
	}
	if strings.Contains(message, "certificate") || strings.Contains(message, "x509:") {
		return "certificate"
	}
	if strings.Contains(message, "clienthello") {
		return "client_hello"
	}
	return "probe_unmeasured"
}

type activeEvidence struct {
	Result   ProbeResult
	Executed bool
	Local    bool
}
type activeProbeFunc func(context.Context, Config, DomainScope, Target, string) activeEvidence

func domainTarget(c Config, s DomainScope) Target {
	for _, p := range c.Profiles {
		for _, target := range p.Targets {
			u, err := url.Parse(target.URL)
			if err == nil && u.Hostname() == s.Host && ((s.Port == 443 && (u.Port() == "" || u.Port() == "443")) || u.Port() == fmt.Sprint(s.Port)) {
				return target
			}
		}
	}
	host := s.Host
	if s.Port != 443 {
		host = net.JoinHostPort(host, fmt.Sprint(s.Port))
	}
	return Target{URL: "https://" + host + "/", Status: 200}
}
func activeLocalError(result ProbeResult) bool {
	// HTTP/auth/rate-limit/body errors, DNS and certificate failures do not
	// establish a DPI failure. No method is rewarded for a 403/451 response.
	if result.OK {
		return false
	}
	if result.Status != 0 {
		return true
	}
	text := strings.ToLower(result.Error)
	for _, part := range []string{"dns:", "непубличный", "no such host", "certificate", "x509:", "operation not permitted", "network is unreachable", "cannot assign requested address", "address family not supported", "protocol not available", "no such device", "permission denied"} {
		if strings.Contains(text, part) {
			return true
		}
	}
	return false
}
func (m *Manager) activeProbe(ctx context.Context, c Config, s DomainScope, target Target, id string) activeEvidence {
	if m.activeProbeHook != nil {
		return m.activeProbeHook(ctx, c, s, target, id)
	}
	direct := id == "direct"
	if m.activeHello == nil {
		return activeEvidence{Local: true, Result: ProbeResult{Error: "Нет формы ошибочного ClientHello"}}
	}
	sample := *m.activeHello
	if time.Now().Unix()-sample.At > 600 {
		return activeEvidence{Local: true, Result: ProbeResult{Error: "ClientHello клиента устарел; применение остановлено"}}
	}
	start := time.Now()
	prepare := func() (string, error) {
		if direct {
			return "", nil
		}
		return trialCommand(c, s, id)
	}
	nonce, err := prepare()
	if err != nil {
		return activeEvidence{Local: true, Result: ProbeResult{Error: err.Error()}}
	}
	err = probeClientHello(ctx, c, sample, direct)
	executed := direct || trialProof(c, s, id, nonce)
	if err != nil {
		result := ProbeResult{URL: target.URL, TLS: map[string]string{"tls12": "1.2", "tls13": "1.3"}[s.TLS], Family: map[string]string{"ipv4": "tcp4", "ipv6": "tcp6"}[s.Family], Direct: direct, Peer: sample.Peer, Error: "ClientHello клиента: " + err.Error(), DurationMS: time.Since(start).Milliseconds()}
		return activeEvidence{Result: result, Executed: executed, Local: errors.Is(err, errHelloUnmeasured) || activeLocalError(result)}
	}
	if !executed {
		return activeEvidence{Result: ProbeResult{URL: target.URL, ClientHello: true, Peer: sample.Peer, Error: "Нет подтверждения исполнения пробы ClientHello"}}
	}
	nonce, err = prepare()
	if err != nil {
		return activeEvidence{Local: true, Result: ProbeResult{Error: err.Error()}}
	}
	version := uint16(tls.VersionTLS13)
	if s.TLS == "tls12" {
		version = tls.VersionTLS12
	}
	family := "tcp4"
	if s.Family == "ipv6" {
		family = "tcp6"
	}
	result := (HTTPProber{MarkDirect: true, TrialMark: !direct, Interface: c.WAN, PinnedIP: sample.Peer}).probe(ctx, target, family, version, time.Duration(c.TimeoutSeconds)*time.Second, direct)
	result.ClientHello = true
	result.Peer = sample.Peer
	result.DurationMS = time.Since(start).Milliseconds()
	return activeEvidence{Result: result, Executed: direct || trialProof(c, s, id, nonce), Local: activeLocalError(result)}
}
func (m *Manager) domainStage(stage, id string) {
	m.mu.Lock()
	m.domainJob.Stage = stage
	m.domainJob.Candidate = id
	m.mu.Unlock()
}
func (m *Manager) domainReason(reason string) {
	m.mu.Lock()
	m.domainJob.Reason = activeReasonCode(reason)
	m.mu.Unlock()
}
func (m *Manager) domainRecord(phase, id string, results []ProbeResult, passed bool, message string) {
	m.mu.Lock()
	m.domainJob.Trials = append(m.domainJob.Trials, Trial{Strategy: phase + ":" + id, Passed: passed, Results: append([]ProbeResult{}, results...), Error: message})
	m.mu.Unlock()
}
func (m *Manager) domainFinish(stage, selected, message string, next time.Time) {
	now := time.Now().UTC()
	m.mu.Lock()
	m.domainJob.Stage = stage
	m.domainJob.Selected = selected
	m.domainJob.Error = message
	m.domainJob.Finished = &now
	if !next.IsZero() {
		t := next.UTC()
		m.domainJob.NextAt = &t
	}
	job := *m.domainJob
	m.mu.Unlock()
	if err := WriteJSON(filepath.Join(filepath.Dir(m.path), "last-domain-search.json"), job); err != nil {
		m.log.Add("Отчёт автоподбора: " + err.Error())
	}
	m.log.Add("Автоподбор " + job.Scope.Host + " " + job.Scope.TLS + " " + job.Scope.Family + ": " + stage + " " + selected)
}
func (m *Manager) searchDomain(ctx context.Context, c Config, s DomainScope) (string, time.Time) {
	retry := time.Now().Add(10 * time.Minute)
	defer atomicControl(filepath.Join(c.RuntimeDir, "probe-command.tsv"), "PILOT_TRIAL1\n")
	finish := func(stage, selected, message string, next time.Time) (string, time.Time) {
		m.domainFinish(stage, selected, message, next)
		return stage, next
	}
	task := m.domainTasks[s]
	if task == nil {
		task = &domainTask{Scope: s}
	}
	fingerprint := domainFingerprint(c, s)
	if task.Fingerprint != fingerprint {
		task.Pool = nil
		task.Cursor = 0
		task.ProofFailures = 0
		task.Fingerprint = fingerprint
	}
	interrupted := func() (string, time.Time) {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			m.domainReason("time_slice")
			return finish("waiting", "", "Отрезок поиска завершён по времени; очередь продолжит с текущего кандидата", time.Now().Add(time.Minute))
		}
		return finish("cancelled", "", "Поиск отменён", time.Time{})
	}
	notMeasured := func(message string) (string, time.Time) {
		m.mu.Lock()
		reason := m.domainJob.Reason
		m.mu.Unlock()
		if reason == "resources" {
			return finish("waiting", "", message, time.Now().Add(30*time.Second))
		}
		if reason == "execution_unconfirmed" {
			// Retry this same candidate, with bounded backoff. A temporary lack
			// of engine proof must not hold the entire search for 15 minutes.
			task.ProofFailures++
			delay := 30 * time.Second
			for i := 1; i < task.ProofFailures && delay < 5*time.Minute; i++ {
				delay *= 2
			}
			if delay > 5*time.Minute {
				delay = 5 * time.Minute
			}
			return finish("unmeasured", "", message, time.Now().Add(delay))
		}
		if reason == "client_hello" {
			return finish("unmeasured", "", message, time.Now().Add(time.Minute))
		}
		return finish("unmeasured", "", message, time.Now().Add(15*time.Minute))
	}
	if until := m.domainBudget(c, s, time.Now().Unix()); until > 0 {
		m.domainReason("switch_budget")
		next := time.Unix(until, 0)
		return finish("waiting", "", "Достигнут лимит применения; пробные подключения не запускаются", next)
	}
	if m.activeProbeHook == nil {
		sample, err := readHelloSample(c, s)
		if err != nil {
			m.domainReason("client_hello_missing")
			return finish("unmeasured", "", err.Error(), time.Now().Add(time.Minute))
		}
		m.activeHello = &sample
		defer func() { m.activeHello = nil }()
	}
	target := domainTarget(c, s)
	check := func(id, phase string, attempts int) (bool, bool, string) {
		m.domainStage(phase, id)
		results := []ProbeResult{}
		passed := true
		measured := true
		message := ""
		for i := 1; i <= attempts; i++ {
			if ctx.Err() != nil {
				measured = false
				passed = false
				message = "Поиск отменён или исчерпал время"
				break
			}
			if !m.backgroundAllowed(c) {
				m.domainReason("resources")
				measured = false
				passed = false
				message = "Поиск ждёт свободных ресурсов роутера"
				break
			}
			evidence := m.activeProbe(ctx, c, s, target, id)
			evidence.Result.Attempt = i
			results = append(results, evidence.Result)
			if evidence.Local && !evidence.Result.OK {
				m.domainReason(activeProbeReason(evidence.Result))
				measured = false
				passed = false
				message = "Проба не измеряет обход: " + evidence.Result.Error
				break
			}
			if !evidence.Executed {
				m.domainReason("execution_unconfirmed")
				measured = false
				passed = false
				message = "Движок не подтвердил исполнение пробного метода"
				break
			}
			if id != "direct" {
				task.ProofFailures = 0
			}
			if !evidence.Result.OK {
				passed = false
				if phase != "direct" {
					break
				}
			}
		}
		if len(results) != attempts {
			passed = false
		}
		m.domainRecord(phase, id, results, passed, message)
		return passed, measured, message
	}
	direct, measured, message := check("direct", "direct", 2)
	if ctx.Err() != nil {
		return interrupted()
	}
	if !measured {
		if ctx.Err() != nil {
			return interrupted()
		}
		return notMeasured(message)
	}
	if direct {
		if err := m.commitDomain(c, s, "none"); err != nil {
			return finish("error", "", err.Error(), retry)
		}
		return finish("applied", "none", "Две прямые HTTPS-пробы прошли", time.Time{})
	}
	// A mixed direct result is a transient or ambiguous line, not proof that a
	// different strategy is needed. Keep serving the previous live method.
	m.mu.Lock()
	baseline := append([]ProbeResult{}, m.domainJob.Trials[0].Results...)
	m.mu.Unlock()
	for _, r := range baseline {
		if r.OK {
			m.domainReason("mixed_direct")
			return finish("unmeasured", "", "Прямые пробы расходятся; рабочий метод сохранён", time.Now().Add(time.Minute))
		}
	}
	p := profileForDomain(c, s.Host)
	p.Domains = []string{s.Host}
	p.Targets = []Target{target}
	if len(task.Pool) == 0 {
		for _, candidate := range tuningStrategies(c, p, ReadAdaptive(c), time.Now().Unix()) {
			if candidate.ID != "none" {
				task.Pool = append(task.Pool, candidate.ID)
			}
		}
	}
	for index := task.Cursor; index < len(task.Pool); index++ {
		candidate, exists := strategyFor(c, task.Pool[index])
		if !exists {
			task.Cursor = index + 1
			continue
		}
		task.Cursor = index
		m.mu.Lock()
		m.domainJob.CandidateIndex = index + 1
		m.domainJob.CandidateTotal = len(task.Pool)
		m.mu.Unlock()
		if ctx.Err() != nil {
			return interrupted()
		}
		passed, measured, message := check(candidate.ID, "search", 1)
		if ctx.Err() != nil {
			return interrupted()
		}
		if !measured {
			if ctx.Err() != nil {
				return interrupted()
			}
			return notMeasured(message)
		}
		if !passed {
			task.Cursor = index + 1
			continue
		}
		attempts := c.Attempts
		if attempts < 2 {
			attempts = 2
		}
		confirmed, measured, message := check(candidate.ID, "confirm", attempts)
		if ctx.Err() != nil {
			return interrupted()
		}
		if !measured {
			if ctx.Err() != nil {
				return interrupted()
			}
			return notMeasured(message)
		}
		if !confirmed {
			task.Cursor = index + 1
			continue
		}
		if !m.backend.Running() {
			return finish("error", "", "Движок остановился до применения", retry)
		}
		if err := m.commitDomain(c, s, candidate.ID); err != nil {
			return finish("error", "", err.Error(), retry)
		}
		task.Cursor = 0
		task.Pool = nil
		return finish("applied", candidate.ID, "HTTPS подтверждён отдельными соединениями", time.Time{})
	}
	task.Cursor = 0
	task.Pool = nil
	return finish("exhausted", "", "Все доступные методы проверены; прежний метод сохранён", retry)
}

func (m *Manager) watchActive(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.activeTick(ctx)
		}
	}
}

func servicePriority(c Config, s DomainScope) int {
	for _, p := range c.Profiles {
		for _, domain := range p.Domains {
			if hostMatches(s.Host, domain) {
				return 0
			}
		}
	}
	return 1
}
func (m *Manager) activeTick(parent context.Context) {
	if m.updateRequested.Load() {
		return
	}
	if !m.op.TryLock() {
		return
	}
	defer m.op.Unlock()
	if m.updateRequested.Load() {
		return
	}
	c := m.Config()
	if m.backend.Kind() != "router" || !c.Enabled || !c.Adaptive || !c.AdaptiveActive || !m.backend.Running() {
		return
	}
	if err := m.syncDomainPlans(c); err != nil {
		m.log.Add("Подтверждённые методы: " + err.Error())
		return
	}
	data := ReadAdaptive(c)
	if data.Error != "" {
		return
	}
	now := time.Now()
	signals := map[DomainScope]int{}
	latest := map[DomainScope]int64{}
	for _, r := range data.History {
		b := boxContext(r)
		s := DomainScope{r.Host, b.TLS, b.Family, r.Port}
		if r.Status != "failed" || !s.valid() || r.Network != c.WAN || r.Updated > now.Unix() || now.Unix()-r.Updated > 600 || c.excludedBy(s.Host) != "" || c.pinFor(s.Host, s.TLS, s.Family, s.Port) != "" || s.Family == "ipv6" && !c.IPv6 {
			continue
		}
		live := profileForDomain(c, s.Host).Strategy
		if plan, ok := m.planFor(c, s); ok {
			live = plan.Strategy
		}
		if r.Strategy != live {
			continue
		}
		signals[s]++
		if r.EventCount > 1 {
			signals[s]++
		}
		if r.Updated > latest[s] {
			latest[s] = r.Updated
		}
	}
	if m.domainTasks == nil {
		m.domainTasks = map[DomainScope]*domainTask{}
	}
	scopes := make([]DomainScope, 0, len(signals))
	for s := range signals {
		scopes = append(scopes, s)
	}
	sort.Slice(scopes, func(i, j int) bool {
		if a, b := servicePriority(c, scopes[i]), servicePriority(c, scopes[j]); a != b {
			return a < b
		}
		return scopes[i].key() < scopes[j].key()
	})
	for _, s := range scopes {
		task := m.domainTasks[s]
		if task == nil && len(m.domainTasks) >= 64 {
			var oldest *domainTask
			for _, candidate := range m.domainTasks {
				if candidate.Handled >= candidate.Signal && (oldest == nil || candidate.Signal < oldest.Signal) {
					oldest = candidate
				}
			}
			// A full queue of unrelated unresolved sites must not prevent a
			// configured service from entering the search at all.
			if oldest == nil && servicePriority(c, s) == 0 {
				for _, candidate := range m.domainTasks {
					if servicePriority(c, candidate.Scope) > 0 && (oldest == nil || candidate.Signal < oldest.Signal) {
						oldest = candidate
					}
				}
			}
			if oldest != nil {
				delete(m.domainTasks, oldest.Scope)
			}
		}
		if task == nil && len(m.domainTasks) < 64 {
			task = &domainTask{Scope: s}
			m.domainTasks[s] = task
		}
		if task != nil && latest[s] > task.Signal {
			task.Signal = latest[s]
		}
	}
	pending := []*domainTask{}
	for s, task := range m.domainTasks {
		if now.Unix()-task.Signal > 6*3600 {
			delete(m.domainTasks, s)
			continue
		}
		if task.Signal > task.Handled && c.excludedBy(s.Host) == "" && c.pinFor(s.Host, s.TLS, s.Family, s.Port) == "" {
			pending = append(pending, task)
		}
	}
	sort.Slice(pending, func(i, j int) bool {
		if a, b := servicePriority(c, pending[i].Scope), servicePriority(c, pending[j].Scope); a != b {
			return a < b
		}
		if !pending[i].Attempted.Equal(pending[j].Attempted) {
			return pending[i].Attempted.Before(pending[j].Attempted)
		}
		if pending[i].Signal != pending[j].Signal {
			return pending[i].Signal < pending[j].Signal
		}
		return pending[i].Scope.key() < pending[j].Scope.key()
	})
	m.mu.Lock()
	m.domainQueue = len(pending)
	closed := m.closed
	m.mu.Unlock()
	if closed || !m.backgroundAllowed(c) {
		return
	}
	var task *domainTask
	for _, candidate := range pending {
		if !now.Before(candidate.Next) {
			task = candidate
			break
		}
	}
	if task == nil {
		return
	}
	task.Attempted = now
	ctx, cancel := context.WithTimeout(parent, 6*time.Minute)
	defer cancel()
	m.mu.Lock()
	m.cancel = cancel
	m.domainJob = &DomainSearch{Scope: task.Scope, Stage: "direct", Started: now.UTC(), Trials: []Trial{}}
	m.mu.Unlock()
	stage, next := m.searchDomain(ctx, c, task.Scope)
	task.Next = next
	if stage == "applied" || stage == "cancelled" {
		task.Cursor = 0
		task.Pool = nil
		task.Handled = now.Unix()
	}
	m.mu.Lock()
	m.cancel = nil
	m.mu.Unlock()
}

func (m *Manager) QueueDomain(s DomainScope) error {
	if !s.valid() {
		return errors.New("неверный домен, TLS, семейство IP или порт")
	}
	if !m.op.TryLock() {
		return ErrBusy
	}
	defer m.op.Unlock()
	c := m.Config()
	if m.backend.Kind() != "router" || !c.Enabled || !c.Adaptive || !c.AdaptiveActive {
		return errors.New("включите полный автоподбор на роутере")
	}
	if c.excludedBy(s.Host) != "" || c.pinFor(s.Host, s.TLS, s.Family, s.Port) != "" {
		return errors.New("домен приостановлен или метод закреплён")
	}
	if s.Family == "ipv6" && !c.IPv6 {
		return errors.New("IPv6 выключен")
	}
	if m.domainTasks == nil {
		m.domainTasks = map[DomainScope]*domainTask{}
	}
	if _, exists := m.domainTasks[s]; !exists && len(m.domainTasks) >= 64 {
		return errors.New("очередь заполнена; дождитесь завершения поиска")
	}
	m.domainTasks[s] = &domainTask{Scope: s, Signal: time.Now().Unix()}
	queued := 0
	for _, task := range m.domainTasks {
		if task.Signal > task.Handled {
			queued++
		}
	}
	m.mu.Lock()
	m.domainQueue = queued
	m.mu.Unlock()
	return nil
}
