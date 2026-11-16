package pilot

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// DiagnosticReport is an explicit export allowlist. Never embed Config, Job,
// Snapshot, logs, or arbitrary error text: they can contain URLs or credentials.
type DiagnosticReport struct {
	SchemaVersion        int                     `json:"schema_version"`
	GeneratedAt          time.Time               `json:"generated_at"`
	Version              string                  `json:"version"`
	Platform             string                  `json:"platform"`
	Service              DiagnosticService       `json:"service"`
	Settings             DiagnosticSettings      `json:"settings"`
	Profiles             []DiagnosticProfile     `json:"profiles"`
	Methods              []DiagnosticMethod      `json:"methods"`
	Rules                DiagnosticRules         `json:"rules"`
	Generation           uint64                  `json:"generation"`
	ObservationReadError bool                    `json:"observation_read_error"`
	Storage              []DiagnosticSlot        `json:"storage"`
	Current              []DiagnosticObservation `json:"current"`
	History              []DiagnosticObservation `json:"history"`
	Discovered           []string                `json:"discovered"`
	Notes                []string                `json:"notes"`
}
type DiagnosticService struct {
	Mode     string `json:"mode"`
	Enabled  bool   `json:"enabled"`
	Running  bool   `json:"running"`
	Busy     bool   `json:"busy"`
	HasError bool   `json:"has_error"`
}
type DiagnosticSettings struct {
	WAN               string `json:"wan"`
	IPv6              bool   `json:"ipv6"`
	PPE               bool   `json:"ppe"`
	QUIC              bool   `json:"quic"`
	DiscordVoice      bool   `json:"discord_voice"`
	Adaptive          bool   `json:"adaptive"`
	Reuse             bool   `json:"reuse"`
	CooldownSeconds   int    `json:"cooldown_seconds"`
	MaxSwitches       int    `json:"max_switches_per_ten_minutes"`
	GlobalMaxSwitches int    `json:"global_max_switches_per_ten_minutes"`
	ProbeLoadGuard    bool   `json:"probe_load_guard"`
	CatalogRevision   uint64 `json:"catalog_revision"`
}
type DiagnosticProfile struct {
	ID       string   `json:"id"`
	Domains  []string `json:"domains"`
	Strategy string   `json:"strategy"`
}
type DiagnosticMethod struct {
	ID     string `json:"id"`
	Family string `json:"family"`
}
type DiagnosticPin struct {
	Host     string `json:"host"`
	Strategy string `json:"strategy"`
	TLS      string `json:"tls"`
	Family   string `json:"family"`
	Port     int    `json:"port"`
}
type DiagnosticRules struct {
	Pins       []DiagnosticPin `json:"pins"`
	Paused     []string        `json:"paused_domains"`
	Exclusions []string        `json:"exclusions"`
}
type DiagnosticSlot struct {
	Slot       int    `json:"slot"`
	State      string `json:"state"`
	Generation uint64 `json:"generation"`
	Records    int    `json:"records"`
	Events     int    `json:"events"`
}
type DiagnosticObservation struct {
	EventCount    int64  `json:"event_count"`
	FirstUpdated  int64  `json:"first_updated"`
	Group         string `json:"group"`
	Box           string `json:"box"`
	Host          string `json:"host"`
	TLS           string `json:"tls"`
	Family        string `json:"family"`
	Network       string `json:"network"`
	Port          int    `json:"port"`
	Strategy      string `json:"observed_strategy"`
	StrategyKnown bool   `json:"observed_strategy_known"`
	Status        string `json:"observed_status"`
	Reason        string `json:"reason"`
	Symptom       string `json:"symptom"`
	Origin        string `json:"origin"`
	Updated       int64  `json:"updated"`
	Pinned        string `json:"pinned,omitempty"`
	Paused        bool   `json:"paused"`
	LastSwitch    int64  `json:"last_switch"`
	WindowStart   int64  `json:"window_start"`
	Switches      int    `json:"switches"`
	HoldUntil     int64  `json:"recorded_hold_until"`
	HoldReason    string `json:"recorded_hold_reason"`
}

func diagnosticObservation(r AdaptiveHost, s Snapshot, now int64, history bool) DiagnosticObservation {
	b := boxContext(r)
	_, known := strategyFor(s.Config, r.Strategy)
	d := DiagnosticObservation{
		EventCount: r.EventCount, FirstUpdated: r.FirstUpdated, Group: r.Group, Box: b.ID, Host: r.Host, TLS: b.TLS, Family: b.Family, Network: r.Network, Port: r.Port,
		Strategy: r.Strategy, StrategyKnown: known, Status: r.Status, Reason: r.Status,
		Symptom: r.Symptom, Origin: r.Origin, Updated: r.Updated,
		LastSwitch: r.LastSwitch, WindowStart: r.WindowStart, Switches: r.Switches,
		HoldUntil: r.HoldUntil, HoldReason: r.HoldReason,
	}
	if history {
		if r.Status == "held" {
			d.Reason = r.HoldReason
		}
		return d
	}
	d.Pinned, d.Paused = r.Pinned, r.Paused
	switch {
	case r.Paused:
		d.Reason = "paused"
	case r.Pinned != "" && r.Pinned != r.Strategy:
		d.Reason = "pinned_pending"
	case !s.Running:
		d.Reason = "service_stopped"
	case r.Pinned != "":
		d.Reason = "pinned"
	case !s.Config.Adaptive:
		d.Reason = "adaptive_disabled"
	case r.HoldUntil > now && (r.HoldReason == "cooldown" || r.HoldReason == "limit"):
		d.Reason = r.HoldReason
	}
	return d
}

func (m *Manager) Diagnostics() DiagnosticReport {
	s := m.Snapshot()
	now := time.Now().UTC()
	a := ReadAdaptive(s.Config)
	d := DiagnosticReport{
		SchemaVersion: 1, GeneratedAt: now, Version: s.Version, Platform: runtime.GOOS + "/" + runtime.GOARCH,
		Service:  DiagnosticService{Mode: s.Mode, Enabled: s.Config.Enabled, Running: s.Running, Busy: s.Busy, HasError: s.Error != ""},
		Settings: DiagnosticSettings{GlobalMaxSwitches: s.Config.AdaptiveGlobalMaxSwitches, ProbeLoadGuard: s.Config.ProbeLoadGuard, WAN: s.Config.WAN, IPv6: s.Config.IPv6, PPE: s.Config.PPE, QUIC: s.Config.QUIC, DiscordVoice: s.Config.DiscordVoice, Adaptive: s.Config.Adaptive, Reuse: s.Config.AdaptiveReuse, CooldownSeconds: s.Config.AdaptiveCooldownSeconds, MaxSwitches: s.Config.AdaptiveMaxSwitches, CatalogRevision: s.Config.CatalogRevision},
		Profiles: []DiagnosticProfile{}, Methods: []DiagnosticMethod{},
		Rules:      DiagnosticRules{Pins: []DiagnosticPin{}, Paused: append([]string{}, s.Config.PausedDomains...), Exclusions: append([]string{}, s.Config.Exclusions...)},
		Generation: a.Generation, ObservationReadError: a.Error != "", Storage: []DiagnosticSlot{},
		Current: []DiagnosticObservation{}, History: []DiagnosticObservation{}, Discovered: append([]string{}, a.Discovered...),
		Notes: []string{
			"Файл содержит домены, имя WAN-интерфейса и историю соединений; он не анонимный. Пароли, токены, журналы, контрольные URL и свободный текст ошибок исключены.",
			"Снимок всех сохранённых контекстов, независимо от фильтров панели. Выгрузка не запускает пробы, не перезапускает движок и не меняет firewall.",
			"Наблюдения и файлы проверяются при возможной записи движком. История ограничена сохранёнными событиями и не является полным журналом трафика.",
			"connected означает наблюдаемую передачу данных, без проверки содержимого HTTPS, видео и голоса. Окончание паузы разрешает смену после новых сбоев; таймер сам подбор не запускает.",
			"reason описывает действующее правило или сохранённый результат. Причины в history относятся к прошлым событиям; recorded_hold_until может быть в прошлом. Пустые новые файлы имеют статус invalid_or_incomplete.",
		},
	}
	for _, p := range s.Config.Profiles {
		d.Profiles = append(d.Profiles, DiagnosticProfile{ID: p.ID, Domains: append([]string{}, p.Domains...), Strategy: p.Strategy})
	}
	for _, method := range s.Strategies {
		d.Methods = append(d.Methods, DiagnosticMethod{ID: method.ID, Family: StrategyFamily(method)})
	}
	for _, pin := range s.Config.DomainPins {
		d.Rules.Pins = append(d.Rules.Pins, DiagnosticPin{Host: pin.Host, Strategy: pin.Strategy, TLS: pin.TLS, Family: pin.Family, Port: pin.Port})
	}
	for _, r := range a.Hosts {
		d.Current = append(d.Current, diagnosticObservation(r, s, now.Unix(), false))
	}
	for _, r := range a.History {
		d.History = append(d.History, diagnosticObservation(r, s, now.Unix(), true))
	}
	for slot := 0; slot < 2; slot++ {
		generation, records, events, err := readAdaptiveSlot(filepath.Join(s.Config.RuntimeDir, fmt.Sprintf("state%d.tsv", slot)))
		check := DiagnosticSlot{Slot: slot, State: "valid", Generation: generation, Records: len(records), Events: len(events)}
		var pathError *os.PathError
		if os.IsNotExist(err) {
			check.State = "missing"
		} else if errors.As(err, &pathError) {
			check.State = "unreadable"
		} else if err != nil {
			check.State = "invalid_or_incomplete"
		}
		d.Storage = append(d.Storage, check)
	}
	return d
}
