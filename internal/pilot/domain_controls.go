package pilot

import (
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
)

// Pins apply to an exact hostname and a TLS/IP/port context. Pauses, like
// exclusions, also cover subdomains, and take precedence over a stored pin.
type DomainPin struct {
	Host     string `json:"host"`
	Strategy string `json:"strategy"`
	TLS      string `json:"tls"`
	Family   string `json:"family"`
	Port     int    `json:"port"`
}

type DomainAction struct {
	Host     string `json:"host"`
	Action   string `json:"action"`
	Strategy string `json:"strategy,omitempty"`
	TLS      string `json:"tls,omitempty"`
	Family   string `json:"family,omitempty"`
	Port     int    `json:"port,omitempty"`
}

func samePinScope(a, b DomainPin) bool {
	return a.Host == b.Host && a.TLS == b.TLS && a.Family == b.Family && a.Port == b.Port
}
func pinMatches(p DomainPin, host, tls, family string, port int) bool {
	return p.Host == host && (p.TLS == "any" || p.TLS == tls) && (p.Family == "any" || p.Family == family) && (p.Port == 0 || p.Port == port)
}
func (c Config) pinFor(host, tls, family string, port int) string {
	for _, p := range c.DomainPins {
		if pinMatches(p, host, tls, family, port) {
			return p.Strategy
		}
	}
	return ""
}
func (c Config) excludedBy(host string) string {
	for _, list := range [][]string{c.Exclusions, c.PausedDomains} {
		for _, d := range list {
			if hostMatches(host, d) {
				return d
			}
		}
	}
	return ""
}
func (c Config) validateDomainControls() error {
	if c.AdaptiveGlobalMaxSwitches < 1 || c.AdaptiveGlobalMaxSwitches > 300 {
		return errors.New("общий лимит смен: 1–300 за 10 минут")
	}
	if c.AdaptiveCooldownSeconds < 10 || c.AdaptiveCooldownSeconds > 300 || c.AdaptiveMaxSwitches < 1 || c.AdaptiveMaxSwitches > 20 {
		return errors.New("пауза смены: 10–300 секунд; лимит смен: 1–20 за 10 минут")
	}
	if len(c.DomainPins) > 128 || len(c.PausedDomains) > 300 {
		return errors.New("не более 128 закреплений и 300 приостановленных доменов")
	}
	paused := map[string]bool{}
	for _, d := range c.PausedDomains {
		if !ValidDomain(d) || paused[d] {
			return errors.New("некорректный или повторный приостановленный домен")
		}
		paused[d] = true
	}
	for i, p := range c.DomainPins {
		if !ValidDomain(p.Host) || !slices.Contains([]string{"any", "tls12", "tls13", "legacy", "unknown"}, p.TLS) || !slices.Contains([]string{"any", "ipv4", "ipv6"}, p.Family) || !slices.Contains([]int{0, 443, 2053, 2083, 2087, 2096, 8443}, p.Port) {
			return errors.New("некорректная область закрепления")
		}
		if _, ok := strategyFor(c, p.Strategy); !ok {
			return fmt.Errorf("неизвестный закреплённый метод %q", p.Strategy)
		}
		for _, prev := range c.DomainPins[:i] {
			if prev.Host == p.Host && (prev.TLS == p.TLS || prev.TLS == "any" || p.TLS == "any") && (prev.Family == p.Family || prev.Family == "any" || p.Family == "any") && (prev.Port == p.Port || prev.Port == 0 || p.Port == 0) {
				return errors.New("пересекающиеся закрепления одного домена")
			}
		}
	}
	return nil
}

func profileControlReason(c Config, p Profile) string {
	if c.YouTubeStable && p.ID == "youtube" { return "YouTube закреплён: выключите стабильный режим перед общим подбором" }
	for _, pin := range c.DomainPins {
		for _, d := range p.Domains {
			if hostMatches(pin.Host, d) {
				return "у профиля есть закреплённые методы; снимите закрепления перед общим подбором"
			}
		}
	}
	for _, target := range p.Targets { // Targets are validated HTTPS URLs.
		u, _ := url.Parse(target.URL)
		host := u.Hostname()
		if c.excludedBy(host) != "" {
			return "контрольный адрес профиля приостановлен; возобновите обработку перед подбором"
		}
	}
	return ""
}

func (m *Manager) ControlDomain(a DomainAction) error {
	a.Host = strings.ToLower(strings.TrimSpace(a.Host))
	if a.Action == "reset_all" {
		if a.Host != "" { return errors.New("сброс всех коробок не принимает домен") }
		if !m.op.TryLock() { return ErrBusy }
		defer m.op.Unlock()
		return m.resetDomainLocked(m.Config(), "")
	}
	if !ValidDomain(a.Host) {
		return errors.New("требуется корректный домен")
	}
	if !m.op.TryLock() {
		return ErrBusy
	}
	defer m.op.Unlock()
	c := m.Config()
	if a.Action == "reset" {
		return m.resetDomainLocked(c, a.Host)
	}
	pin := DomainPin{Host: a.Host, Strategy: a.Strategy, TLS: a.TLS, Family: a.Family, Port: a.Port}
	if pin.TLS == "" {
		pin.TLS = "any"
	}
	if pin.Family == "" {
		pin.Family = "any"
	}
	switch a.Action {
	case "pin":
		c.DomainPins = slices.DeleteFunc(c.DomainPins, func(p DomainPin) bool { return samePinScope(p, pin) })
		c.DomainPins = append(c.DomainPins, pin)
	case "unpin":
		c.DomainPins = slices.DeleteFunc(c.DomainPins, func(p DomainPin) bool { return samePinScope(p, pin) })
	case "pause":
		if !slices.Contains(c.PausedDomains, a.Host) {
			c.PausedDomains = append(c.PausedDomains, a.Host)
		}
	case "resume":
		c.PausedDomains = slices.DeleteFunc(c.PausedDomains, func(d string) bool { return d == a.Host })
	default:
		return errors.New("неизвестное действие с доменом")
	}
	if err := c.Validate(); err != nil {
		return err
	}
	if err := m.changeLocked(c); err != nil {
		return err
	}
	m.log.Add("Управление доменом: " + a.Host + " · " + a.Action)
	return nil
}
