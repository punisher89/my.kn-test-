package pilot

import (
	"bufio"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type DomainScope struct {
	Host   string `json:"host"`
	TLS    string `json:"tls"`
	Family string `json:"family"`
	Port   int    `json:"port"`
}

func (s DomainScope) key() string { return fmt.Sprintf("%s/%s/%s/%d", s.Host, s.TLS, s.Family, s.Port) }
func (s DomainScope) valid() bool {
	return ValidDomain(s.Host) && (s.TLS == "tls12" || s.TLS == "tls13") && (s.Family == "ipv4" || s.Family == "ipv6") && (s.Port == 443 || s.Port == 2053 || s.Port == 2083 || s.Port == 2087 || s.Port == 2096 || s.Port == 8443)
}

type DomainPlan struct {
	Scope       DomainScope `json:"scope"`
	Strategy    string      `json:"strategy"`
	Fingerprint string      `json:"fingerprint"`
	Verified    time.Time   `json:"verified"`
	Expires     time.Time   `json:"expires"`
	Window      int64       `json:"window"`
	Switches    int         `json:"switches"`
	LastSwitch  int64       `json:"last_switch"`
}
type domainPlanStore struct {
	Version       int          `json:"version"`
	Plans         []DomainPlan `json:"plans"`
	GlobalWindow  int64        `json:"global_window"`
	GlobalChanges int          `json:"global_changes"`
}

func profileForDomain(c Config, host string) Profile {
	for _, p := range c.Profiles {
		for _, d := range p.Domains {
			if hostMatches(host, d) {
				return p
			}
		}
	}
	return Profile{ID: "discovered", Strategy: "none", Domains: []string{host}}
}
func activeContext(c Config) string {
	c = cloneConfig(c)
	type profile struct {
		ID, Strategy string
		Domains      []string
	}
	profiles := []profile{}
	for _, p := range c.Profiles {
		profiles = append(profiles, profile{p.ID, p.Strategy, p.Domains})
	}
	data, _ := json.Marshal(struct {
		WAN              string
		IPv6, PPE        bool
		Profiles         []profile
		Catalog          []Strategy
		Pins             []DomainPin
		Paused, Excluded []string
	}{c.WAN, c.IPv6, c.PPE, profiles, c.Catalog, c.DomainPins, c.PausedDomains, c.Exclusions})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
func domainFingerprint(c Config, s DomainScope) string {
	p := profileForDomain(c, s.Host)
	data, _ := json.Marshal(struct {
		Scope                  DomainScope
		WAN, Profile, Strategy string
		Catalog                []Strategy
		Revision               uint64
		IPv6, PPE, QUIC        bool
		Pin, Excluded          string
	}{s, c.WAN, p.ID, p.Strategy, append([]Strategy{}, c.Catalog...), c.CatalogRevision, c.IPv6, c.PPE, c.QUIC, c.pinFor(s.Host, s.TLS, s.Family, s.Port), c.excludedBy(s.Host)})
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}
func (m *Manager) planPath() string {
	return filepath.Join(filepath.Dir(m.path), "verified-domains.json")
}
func (m *Manager) loadDomainPlans() {
	m.domainPlans = domainPlanStore{Version: 1, Plans: []DomainPlan{}}
	f, err := os.Open(m.planPath())
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		m.domainPlanIssue = err.Error()
		return
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	var stored domainPlanStore
	if err != nil || len(data) > 1<<20 || json.Unmarshal(data, &stored) != nil || stored.Version != 1 || len(stored.Plans) > 512 || stored.GlobalWindow < 0 || stored.GlobalChanges < 0 {
		m.domainPlanIssue = "повреждён файл подтверждённых методов"
		return
	}
	seen := map[string]bool{}
	for _, p := range stored.Plans {
		hash, err := hex.DecodeString(p.Fingerprint)
		if !p.Scope.valid() || !groupRE.MatchString(p.Strategy) || err != nil || len(hash) != 32 || p.Verified.IsZero() || !p.Expires.After(p.Verified) || p.Switches < 0 || p.Switches > 1000000 || p.Window < 0 || p.LastSwitch < 0 || seen[p.Scope.key()] {
			m.domainPlanIssue = "повреждён файл подтверждённых методов"
			return
		}
		seen[p.Scope.key()] = true
	}
	m.domainPlans = stored
}
func atomicControl(path, text string) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".pilot-control-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0644); err == nil {
		_, err = f.WriteString(text)
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(name, path)
}
func domainPlansText(c Config, plans []DomainPlan) string {
	sort.Slice(plans, func(i, j int) bool { return plans[i].Scope.key() < plans[j].Scope.key() })
	var text strings.Builder
	text.WriteString("PILOT_VERIFIED1\t" + activeContext(c) + "\n")
	now := time.Now()
	for _, p := range plans {
		if p.Expires.After(now) && !p.Verified.After(now.Add(time.Minute)) && p.Fingerprint == domainFingerprint(c, p.Scope) && c.excludedBy(p.Scope.Host) == "" {
			if _, ok := strategyFor(c, p.Strategy); ok {
				fmt.Fprintf(&text, "%s\t%s\t%s\t%d\t%s\t%d\n", p.Scope.Host, p.Scope.TLS, p.Scope.Family, p.Scope.Port, p.Strategy, p.Expires.Unix())
			}
		}
	}
	return text.String()
}
func (m *Manager) syncDomainPlans(c Config) error {
	m.mu.Lock()
	plans := append([]DomainPlan{}, m.domainPlans.Plans...)
	m.mu.Unlock()
	body := domainPlansText(c, plans)
	if body == m.domainPublished {
		if info, err := os.Stat(filepath.Join(c.RuntimeDir, "verified-domains.tsv")); err == nil && info.Mode().IsRegular() && info.Size() == int64(len(body)) {
			return nil
		}
	}
	if err := atomicControl(filepath.Join(c.RuntimeDir, "verified-domains.tsv"), body); err != nil {
		return err
	}
	m.domainPublished = body
	return nil
}
func (m *Manager) planFor(c Config, s DomainScope) (DomainPlan, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	for _, p := range m.domainPlans.Plans {
		if p.Scope == s && p.Expires.After(now) && p.Fingerprint == domainFingerprint(c, s) {
			return p, true
		}
	}
	return DomainPlan{}, false
}
func (m *Manager) domainBudget(c Config, s DomainScope, now int64) int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.domainPlans.GlobalWindow <= now && m.domainPlans.GlobalWindow+600 > now && m.domainPlans.GlobalChanges >= c.AdaptiveGlobalMaxSwitches {
		return m.domainPlans.GlobalWindow + 600
	}
	for _, p := range m.domainPlans.Plans {
		if p.Scope == s && p.Fingerprint == domainFingerprint(c, s) {
			if p.Window <= now && p.Window+600 > now && p.Switches >= c.AdaptiveMaxSwitches {
				return p.Window + 600
			}
			cooldown := c.AdaptiveCooldownSeconds
			if c.AdaptiveFastStart && p.Switches < 3 {
				cooldown = 10
			}
			if p.LastSwitch <= now && p.LastSwitch+int64(cooldown) > now {
				return p.LastSwitch + int64(cooldown)
			}
		}
	}
	return 0
}
func (m *Manager) commitDomain(c Config, s DomainScope, id string) error {
	if !s.valid() {
		return errors.New("неверный контекст домена")
	}
	if _, ok := strategyFor(c, id); !ok {
		return errors.New("неизвестный метод")
	}
	now := time.Now().UTC()
	if until := m.domainBudget(c, s, now.Unix()); until > now.Unix() {
		return fmt.Errorf("лимит смен до %s", time.Unix(until, 0).Format(time.RFC3339))
	}
	m.mu.Lock()
	store := m.domainPlans
	store.Plans = append([]DomainPlan{}, store.Plans...)
	issue := m.domainPlanIssue
	m.mu.Unlock()
	if issue != "" {
		return errors.New(issue)
	}
	next := DomainPlan{Scope: s, Strategy: id, Fingerprint: domainFingerprint(c, s), Verified: now, Expires: now.Add(24 * time.Hour), Window: now.Unix()}
	current := profileForDomain(c, s.Host).Strategy
	kept := []DomainPlan{}
	for _, p := range store.Plans {
		if p.Scope == s {
			if p.Fingerprint == next.Fingerprint {
				current = p.Strategy
				next.Window = p.Window
				next.Switches = p.Switches
				next.LastSwitch = p.LastSwitch
			}
		} else if p.Expires.After(now) {
			kept = append(kept, p)
		}
	}
	if next.Window > now.Unix() || next.Window+600 <= now.Unix() {
		next.Window = now.Unix()
		next.Switches = 0
	}
	if store.GlobalWindow > now.Unix() || store.GlobalWindow+600 <= now.Unix() {
		store.GlobalWindow = now.Unix()
		store.GlobalChanges = 0
	}
	if current != id {
		next.Switches++
		next.LastSwitch = now.Unix()
		store.GlobalChanges++
	}
	kept = append(kept, next)
	sort.Slice(kept, func(i, j int) bool { return kept[i].Verified.After(kept[j].Verified) })
	if len(kept) > 512 {
		kept = kept[:512]
	}
	store.Plans = kept
	oldBody := m.domainPublished
	if oldBody == "" {
		m.mu.Lock()
		oldPlans := append([]DomainPlan{}, m.domainPlans.Plans...)
		m.mu.Unlock()
		oldBody = domainPlansText(c, oldPlans)
	}
	newBody := domainPlansText(c, append([]DomainPlan{}, store.Plans...))
	controlPath := filepath.Join(c.RuntimeDir, "verified-domains.tsv")
	if err := atomicControl(controlPath, newBody); err != nil {
		return err
	}
	if err := WriteJSON(m.planPath(), store); err != nil {
		return errors.Join(err, atomicControl(controlPath, oldBody))
	}
	m.domainPublished = newBody
	m.mu.Lock()
	m.domainPlans = store
	m.mu.Unlock()
	return nil
}
func trialCommand(c Config, s DomainScope, id string) (string, error) {
	if !s.valid() {
		return "", errors.New("неверный контекст пробы")
	}
	if _, ok := strategyFor(c, id); !ok {
		return "", errors.New("неизвестный кандидат")
	}
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	nonce := hex.EncodeToString(random)
	text := fmt.Sprintf("PILOT_TRIAL1\t%s\t%s\t%s\t%s\t%s\t%d\t%d\n", nonce, s.Host, id, s.TLS, s.Family, s.Port, time.Now().Add(90*time.Second).Unix())
	return nonce, atomicControl(filepath.Join(c.RuntimeDir, "probe-command.tsv"), text)
}
func trialProof(c Config, s DomainScope, id, nonce string) bool {
	f, err := os.Open(filepath.Join(c.RuntimeDir, "probe-proof.tsv"))
	if err != nil {
		return false
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 1025))
	if err != nil || len(data) > 1024 {
		return false
	}
	expected := fmt.Sprintf("PILOT_PROOF1\t%s\t%s\t%s\t%s\t%s\t%d\nEND\t%s\n", nonce, s.Host, id, s.TLS, s.Family, s.Port, nonce)
	return string(data) == expected
}

func runtimeDomainPlans(c Config) map[DomainScope]string {
	result := map[DomainScope]string{}
	f, err := os.Open(filepath.Join(c.RuntimeDir, "verified-domains.tsv"))
	if err != nil {
		return result
	}
	defer f.Close()
	scanner := bufio.NewScanner(io.LimitReader(f, 1<<20))
	scanner.Buffer(make([]byte, 1024), 2048)
	if !scanner.Scan() || scanner.Text() != "PILOT_VERIFIED1\t"+activeContext(c) {
		return result
	}
	for i := 0; i < 512 && scanner.Scan(); i++ {
		fields := strings.Split(scanner.Text(), "\t")
		if len(fields) != 6 {
			continue
		}
		port, err := strconv.Atoi(fields[3])
		if err != nil {
			continue
		}
		scope := DomainScope{fields[0], fields[1], fields[2], port}
		expires, err := strconv.ParseInt(fields[5], 10, 64)
		if err != nil || !scope.valid() || expires <= time.Now().Unix() {
			continue
		}
		if _, ok := strategyFor(c, fields[4]); ok {
			result[scope] = fields[4]
		}
	}
	return result
}
