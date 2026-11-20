package pilot

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Overridden by the release build; development binaries identify themselves.
var Version = "dev"

type Target struct {
	URL      string `json:"url"`
	Status   int    `json:"status"`
	Contains string `json:"contains,omitempty"`
}

type Profile struct {
	DomainSource     string    `json:"domain_source,omitempty"`
	AutoDomains      bool      `json:"auto_domains"`
	DomainSHA256     string    `json:"domain_sha256,omitempty"`
	SourceExclusions []string  `json:"source_exclusions,omitempty"`
	DomainsUpdated   time.Time `json:"domains_updated,omitempty"`
	DomainRevision   uint64    `json:"domain_revision,omitempty"`
	ID               string    `json:"id"`
	Name             string    `json:"name"`
	Domains          []string  `json:"domains"`
	Strategy         string    `json:"strategy"`
	Targets          []Target  `json:"targets"`
}

type Config struct {
	probeFailFast bool

	Adaptive                  bool        `json:"adaptive"`
	AdaptiveActive            bool        `json:"adaptive_active"`
	BackgroundOtherDomains     bool        `json:"background_other_domains"`
	AdaptiveFastStart         bool        `json:"adaptive_fast_start"`
	AdaptiveReuse             bool        `json:"adaptive_reuse"`
	AdaptiveCooldownSeconds   int         `json:"adaptive_cooldown_seconds"`
	AdaptiveMaxSwitches       int         `json:"adaptive_max_switches"`
	AdaptiveGlobalMaxSwitches int         `json:"adaptive_global_max_switches"`
	ProbeLoadGuard            bool        `json:"probe_load_guard"`
	DomainPins                []DomainPin `json:"domain_pins,omitempty"`
	PausedDomains             []string    `json:"paused_domains,omitempty"`
	RuntimeDir                string      `json:"runtime_dir"`
	tuning                    bool

	Catalog         []Strategy `json:"catalog,omitempty"`
	CatalogRevision uint64     `json:"catalog_revision,omitempty"`
	AutoCatalog     bool       `json:"auto_catalog"`

	Listen          string    `json:"listen"`
	WAN             string    `json:"wan"`
	EngineDir       string    `json:"engine_dir"`
	Queue           int       `json:"queue"`
	Enabled         bool      `json:"enabled"`
	IPv6            bool      `json:"ipv6"`
	PPE             bool      `json:"ppe"`
	QUIC            bool      `json:"quic"`
	YouTubeStable   bool      `json:"youtube_stable"`
	TelegramMTProto bool     `json:"telegram_mtproto"`
	DiscordVoice    bool      `json:"discord_voice"`
	Auto            bool      `json:"auto"`
	IntervalMinutes int       `json:"interval_minutes"`
	TimeoutSeconds  int       `json:"timeout_seconds"`
	Attempts        int       `json:"attempts"`
	Exclusions      []string  `json:"exclusions"`
	Profiles        []Profile `json:"profiles"`
}

func DefaultConfig() Config {
	return Config{
		AdaptiveActive: true, AdaptiveFastStart: true, AdaptiveGlobalMaxSwitches: 30, ProbeLoadGuard: true,
		AdaptiveReuse: true, AdaptiveCooldownSeconds: 30, AdaptiveMaxSwitches: 6, RuntimeDir: "/opt/keenetic-pilot-state", Listen: "127.0.0.1:9080", EngineDir: "/opt/keenetic-pilot/engine", Queue: 220,
		AutoCatalog: true, PPE: true, QUIC: true, DiscordVoice: false, Auto: true,
		IntervalMinutes: 15, TimeoutSeconds: 8, Attempts: 2,
		Exclusions: []string{},
		Profiles: []Profile{
			{ID: "youtube", Name: "YouTube", Strategy: "split-sni", Domains: presetDomains("youtube"), Targets: []Target{
				{URL: "https://www.youtube.com/", Status: 200, Contains: "ytInitialData"},
				{URL: "https://i.ytimg.com/vi/jNQXAC9IVRw/default.jpg", Status: 200},
			}},
			{ID: "discord", Name: "Discord", Strategy: "split-sni", Domains: []string{"discord.com", "discord.gg", "discord.media", "discordapp.com", "discordapp.net", "discordcdn.com", "discord.dev"}, Targets: []Target{
				{URL: "https://discord.com/api/v10/gateway", Status: 200, Contains: "wss://gateway.discord.gg"},
			}},
		},
	}
}

var domainRE = regexp.MustCompile(`^(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
var interfaceRE = regexp.MustCompile(`^[a-zA-Z0-9_.:-]{1,15}$`)
var idRE = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

func ValidDomain(s string) bool { return len(s) <= 253 && domainRE.MatchString(s) }

func (c Config) Validate() error {
	if err := c.validateDomainControls(); err != nil {
		return err
	}
	if err := validateCatalog(c.Catalog); err != nil {
		return err
	}

	if !filepath.IsAbs(c.RuntimeDir) || filepath.Clean(c.RuntimeDir) == "/" || strings.ContainsAny(c.RuntimeDir, "\n\r\x00") {
		return errors.New("некорректный runtime_dir")
	}
	if c.Queue < 1 || c.Queue > 65535 {
		return errors.New("queue: требуется 1–65535")
	}
	if c.WAN != "" && !interfaceRE.MatchString(c.WAN) {
		return errors.New("некорректное имя WAN-интерфейса")
	}
	if !filepath.IsAbs(c.EngineDir) || strings.ContainsAny(c.EngineDir, "\n\r\x00") {
		return errors.New("engine_dir: требуется абсолютный путь")
	}
	host, _, err := net.SplitHostPort(c.Listen)
	if err != nil {
		return errors.New("listen: требуется IP:порт")
	}
	ip := net.ParseIP(host)
	if ip == nil || !(ip.IsPrivate() || ip.IsLoopback()) {
		return errors.New("панель разрешено привязывать только к конкретному LAN или loopback IP")
	}
	_, port, _ := net.SplitHostPort(c.Listen)
	portNumber, portErr := strconv.Atoi(port)
	if portErr != nil || portNumber < 1 || portNumber > 65535 {
		return errors.New("listen: нужен фиксированный порт")
	}
	if c.Attempts < 1 || c.Attempts > 3 || c.TimeoutSeconds < 2 || c.TimeoutSeconds > 20 || c.IntervalMinutes < 5 || c.IntervalMinutes > 1440 {
		return errors.New("attempts: 1–3; timeout_seconds: 2–20; interval_minutes: 5–1440")
	}
	if len(c.Profiles) == 0 || len(c.Profiles) > 12 || len(c.Exclusions) > 300 {
		return errors.New("требуется 1–12 профилей; не более 300 исключений")
	}
	for _, d := range c.Exclusions {
		if !ValidDomain(d) {
			return fmt.Errorf("некорректный домен исключения: %q", d)
		}
	}
	ids := map[string]bool{}
	domains := []string{}
	for _, p := range c.Profiles {
		if p.DomainSource != "" {
			if strings.HasPrefix(p.DomainSource, "pilot:") {
				if !idRE.MatchString(strings.TrimPrefix(p.DomainSource, "pilot:")) {
					return errors.New("неверный источник каталога")
				}
			} else if e := sourceURL(p.DomainSource); e != nil {
				return e
			}
		}
		if p.AutoDomains && p.DomainSource == "" {
			return errors.New("для автообновления доменов нужен источник")
		}
		if p.DomainSHA256 != "" {
			if ok, _ := regexp.MatchString(`^[a-f0-9]{64}$`, p.DomainSHA256); !ok {
				return errors.New("SHA-256 списка должен содержать 64 символа hex")
			}
		}
		if len(p.SourceExclusions) > 300 {
			return errors.New("слишком много исключений источника")
		}
		for _, d := range p.SourceExclusions {
			if !ValidDomain(d) {
				return errors.New("неверное исключение источника")
			}
		}

		if !idRE.MatchString(p.ID) || ids[p.ID] || len(p.Name) > 80 {
			return errors.New("ID профилей должны быть уникальны; имя до 80 символов")
		}
		ids[p.ID] = true
		if _, ok := strategyFor(c, p.Strategy); !ok {
			return fmt.Errorf("неизвестная стратегия %q", p.Strategy)
		}
		if len(p.Domains) == 0 || len(p.Domains) > 300 || len(p.Targets) == 0 || len(p.Targets) > 6 {
			return errors.New("профиль: требуется 1–300 доменов и 1–6 проверок")
		}
		for _, d := range p.Domains {
			if !ValidDomain(d) {
				return fmt.Errorf("некорректный домен: %q", d)
			}
			for _, prev := range domains {
				if hostMatches(d, prev) || hostMatches(prev, d) {
					return fmt.Errorf("пересекающиеся домены профилей: %s и %s", prev, d)
				}
			}
			domains = append(domains, d)
		}
		urls := map[string]bool{}
		for _, t := range p.Targets {
			if urls[t.URL] {
				return errors.New("контрольные адреса профиля не должны повторяться")
			}
			urls[t.URL] = true
			u, err := url.Parse(t.URL)
			if err != nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" || u.Port() != "" || !ValidDomain(u.Hostname()) || len(t.URL) > 2048 {
				return errors.New("проверка: требуется публичный HTTPS URL без порта, пароля и фрагмента")
			}
			if t.Status < 200 || t.Status > 399 || len(t.Contains) > 512 {
				return errors.New("проверка: status 200–399, contains до 512 байт")
			}
			if !slices.ContainsFunc(p.Domains, func(d string) bool { return hostMatches(u.Hostname(), d) }) {
				return fmt.Errorf("хост проверки %s отсутствует в доменах профиля", u.Hostname())
			}
			if slices.ContainsFunc(c.Exclusions, func(d string) bool { return hostMatches(u.Hostname(), d) }) {
				return fmt.Errorf("хост проверки %s исключён из обработки", u.Hostname())
			}
		}
	}
	return nil
}

// This only protects explicitly configured YouTube profile domains. It never
// changes the global TLS or MTProto policies of unrelated services.
func stableYouTubeHost(c Config, host string) bool {
	if !c.YouTubeStable { return false }
	for _, p := range c.Profiles {
		if p.ID != "youtube" { continue }
		for _, d := range p.Domains {
			if hostMatches(host, d) { return true }
		}
	}
	return false
}

func hostMatches(host, domain string) bool {
	return host == domain || strings.HasSuffix(host, "."+domain)
}

func LoadConfig(path string) (Config, error) {
	c := DefaultConfig()
	b, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	err = json.Unmarshal(b, &c)
	if err != nil {
		return c, err
	}
	return c, c.Validate()
}

func WriteJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".pilot-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(append(b, '\n'))
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(name, path); err != nil {
		return err
	}
	d, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
