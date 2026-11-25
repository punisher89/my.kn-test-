package pilot

import (
	"bufio"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const adaptiveUID = 2147483647
const adaptiveMarker = "Keenetic Pilot adaptive v1\n"

// Only data files are writable by nfqws2's UID. The directory and Lua code
// remain root-owned; the engine cannot replace files with symlinks.
func PrepareAdaptive(c Config) error {
	dir := c.RuntimeDir
	if info, err := os.Lstat(dir); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("runtime_dir должен быть обычным каталогом")
		}
		marker, err := os.ReadFile(filepath.Join(dir, ".pilot-managed"))
		if err != nil || string(marker) != adaptiveMarker {
			return errors.New("неизвестный каталог adaptive; отказ изменения прав")
		}
	} else if !os.IsNotExist(err) {
		return err
	} else {
		if err = os.MkdirAll(dir, 0711); err != nil {
			return err
		}
		if err = os.WriteFile(filepath.Join(dir, ".pilot-managed"), []byte(adaptiveMarker), 0600); err != nil {
			return err
		}
	}
	if err := os.Chmod(dir, 0711); err != nil {
		return err
	}
	samples := []string{"autohosts.txt", "state0.tsv", "state1.tsv", "probe-proof.tsv"}
	for i := 0; i < 64; i++ {
		samples = append(samples, fmt.Sprintf("hello%02d.tsv", i))
	}
	for _, name := range samples {
		path := filepath.Join(dir, name)
		f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
		if err != nil {
			return err
		}
		info, err := f.Stat()
		if err != nil || !info.Mode().IsRegular() {
			f.Close()
			return fmt.Errorf("необычный файл adaptive: %s", name)
		}
		if os.Geteuid() == 0 {
			err = f.Chown(adaptiveUID, adaptiveUID)
		}
		if err == nil {
			err = f.Chmod(0600)
		}
		f.Close()
		if err != nil {
			return err
		}
	}
	for _, name := range []string{"probe-command.tsv", "verified-domains.tsv"} {
		f, err := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_RDONLY|syscall.O_NOFOLLOW, 0644)
		if err != nil {
			return err
		}
		info, err := f.Stat()
		if err != nil || !info.Mode().IsRegular() {
			f.Close()
			return fmt.Errorf("необычный управляющий файл: %s", name)
		}
		if os.Geteuid() == 0 {
			err = f.Chown(0, 0)
		}
		if err == nil {
			err = f.Chmod(0644)
		}
		f.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

type AdaptiveHost struct {
	Applied      string `json:"applied_strategy,omitempty"`
	Measured     bool   `json:"https_measured,omitempty"`
	EventCount   int64  `json:"event_count"`
	FirstUpdated int64  `json:"first_updated"`
	Group        string `json:"group"`
	Host         string `json:"host"`
	Strategy     string `json:"strategy"`
	Status       string `json:"status"`
	Updated      int64  `json:"updated"`
	Symptom      string `json:"symptom"`
	Network      string `json:"network"`
	Port         int    `json:"port"`
	Origin       string `json:"origin"`
	LastSwitch   int64  `json:"last_switch"`
	WindowStart  int64  `json:"window_start"`
	Switches     int    `json:"switches"`
	HoldUntil    int64  `json:"hold_until"`
	HoldReason   string `json:"hold_reason"`
	Pinned       string `json:"pinned,omitempty"`
	Paused       bool   `json:"paused"`
}
type AdaptiveSnapshot struct {
	Active     bool           `json:"active"`
	Enabled    bool           `json:"enabled"`
	Reuse      bool           `json:"reuse"`
	Boxes      []AdaptiveBox  `json:"boxes"`
	History    []AdaptiveHost `json:"history"`
	Generation uint64         `json:"generation"`
	Hosts      []AdaptiveHost `json:"hosts"`
	Discovered []string       `json:"discovered"`
	Error      string         `json:"error,omitempty"`
}

var groupRE = regexp.MustCompile(`^[a-z0-9_-]{1,96}$`)

func readAdaptiveSlot(path string) (uint64, []AdaptiveHost, []AdaptiveHost, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, nil, nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 524289))
	if err != nil {
		return 0, nil, nil, err
	}
	if len(data) > 524288 {
		return 0, nil, nil, errors.New("слишком большой файл наблюдений")
	}
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	if !scanner.Scan() {
		return 0, nil, nil, io.ErrUnexpectedEOF
	}
	header := strings.Split(scanner.Text(), "\t")
	if len(header) != 2 || (header[0] != "PILOT1" && header[0] != "PILOT2" && header[0] != "PILOT3" && header[0] != "PILOT4") {
		return 0, nil, nil, errors.New("неполный заголовок adaptive")
	}
	generation, err := strconv.ParseUint(header[1], 10, 53)
	if err != nil {
		return 0, nil, nil, err
	}
	hosts := []AdaptiveHost{}
	events := []AdaptiveHost{}
	seen := map[string]bool{}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "END\t"+header[1] {
			if scanner.Scan() {
				return 0, nil, nil, errors.New("лишние данные adaptive")
			}
			return generation, hosts, events, scanner.Err()
		}
		fields := strings.Split(line, "\t")
		kind := "R"
		if header[0] == "PILOT2" || header[0] == "PILOT3" || header[0] == "PILOT4" {
			if header[0] == "PILOT2" && len(fields) != 10 || header[0] == "PILOT3" && len(fields) != 15 || header[0] == "PILOT4" && len(fields) != 17 {
				return 0, nil, nil, errors.New("неполная запись adaptive")
			}
			kind = fields[0]
			fields = fields[1:]
		} else if len(fields) != 5 {
			return 0, nil, nil, errors.New("неполная запись adaptive")
		}
		stamp, err := strconv.ParseInt(fields[4], 10, 64)
		r := AdaptiveHost{EventCount: 1, FirstUpdated: stamp, Group: fields[0], Host: fields[1], Strategy: fields[2], Status: fields[3], Updated: stamp, Symptom: "unknown", Network: "-", Origin: "restored", HoldReason: "none"}
		validStatus := (kind == "R" && (r.Status == "probing" || r.Status == "connected")) || (kind == "E" && (r.Status == "failed" || r.Status == "selected" || r.Status == "connected" || r.Status == "held"))
		if err != nil || stamp < 0 || !groupRE.MatchString(r.Group) || !ValidDomain(r.Host) || !idRE.MatchString(r.Strategy) || !validStatus {
			return 0, nil, nil, errors.New("некорректная запись adaptive")
		}
		if len(fields) >= 9 {
			r.Symptom, r.Network, r.Origin = fields[5], fields[6], fields[8]
			r.Port, err = strconv.Atoi(fields[7])
			if err != nil || r.Port < 0 || r.Port > 65535 || !interfaceRE.MatchString(r.Network) || !allowedSymptom(r.Symptom) || !allowedOrigin(r.Origin) {
				return 0, nil, nil, errors.New("некорректный контекст adaptive")
			}
		}
		if len(fields) >= 14 {
			nums := []int64{}
			for _, v := range fields[9:13] {
				n, e := strconv.ParseInt(v, 10, 53)
				if e != nil || n < 0 {
					return 0, nil, nil, errors.New("некорректный бюджет смены")
				}
				nums = append(nums, n)
			}
			if nums[2] > 1000000 || !slices.Contains([]string{"none", "cooldown", "limit"}, fields[13]) {
				return 0, nil, nil, errors.New("некорректная пауза смены")
			}
			r.LastSwitch, r.WindowStart, r.Switches, r.HoldUntil, r.HoldReason = nums[0], nums[1], int(nums[2]), nums[3], fields[13]
		}
		if header[0] == "PILOT4" {
			count, e1 := strconv.ParseInt(fields[14], 10, 64)
			first, e2 := strconv.ParseInt(fields[15], 10, 64)
			if e1 != nil || e2 != nil || count < 1 || count > 1000000000 || first < 0 || first > stamp || (kind == "R" && (count != 1 || first != stamp)) || (r.Status != "failed" && count != 1) {
				return 0, nil, nil, errors.New("некорректный счётчик событий adaptive")
			}
			r.EventCount, r.FirstUpdated = count, first
		}
		if kind == "E" {
			if len(events) >= 128 {
				return 0, nil, nil, errors.New("слишком большая история adaptive")
			}
			events = append(events, r)
		} else {
			key := r.Group + "\t" + r.Host
			if seen[key] || len(hosts) >= 512 {
				return 0, nil, nil, errors.New("повтор или избыток записей adaptive")
			}
			seen[key] = true
			hosts = append(hosts, r)
		}
	}
	return 0, nil, nil, io.ErrUnexpectedEOF
}

func allowedSymptom(s string) bool {
	return s == "unknown" || s == "rst" || s == "retransmission" || s == "tls_alert"
}
func allowedOrigin(s string) bool {
	return s == "initial" || s == "pool" || s == "learned" || s == "restored" || s == "manual"
}

func readAdaptiveState(c Config) (result AdaptiveSnapshot) {
	result = AdaptiveSnapshot{Enabled: c.Adaptive, Reuse: c.AdaptiveReuse, Hosts: []AdaptiveHost{}, Discovered: []string{}, History: []AdaptiveHost{}, Boxes: []AdaptiveBox{}}
	for i := 0; i < 2; i++ {
		n, hosts, events, err := readAdaptiveSlot(filepath.Join(c.RuntimeDir, fmt.Sprintf("state%d.tsv", i)))
		if err == nil && n > result.Generation {
			result.Generation = n
			result.Hosts = hosts
			result.History = events
		}
	}
	return result
}

func ReadAdaptive(c Config) (result AdaptiveSnapshot) {
	result = readAdaptiveState(c)
	defer func() {
		result.Active = c.Adaptive && c.AdaptiveActive
		plans := map[DomainScope]string{}
		if result.Active {
			plans = runtimeDomainPlans(c)
		}
		for i, r := range result.Hosts {
			b := boxContext(r)
			result.Hosts[i].Pinned = c.pinFor(r.Host, b.TLS, b.Family, r.Port)
			result.Hosts[i].Paused = c.excludedBy(r.Host) != ""
			if result.Active && r.Network == c.WAN {
				id := profileForDomain(c, r.Host).Strategy
				if measured, ok := plans[DomainScope{r.Host, b.TLS, b.Family, r.Port}]; ok {
					id = measured
					result.Hosts[i].Measured = true
				}
				if result.Hosts[i].Pinned != "" {
					id = result.Hosts[i].Pinned
					result.Hosts[i].Measured = false
				}
				if result.Hosts[i].Paused {
					id = "none"
					result.Hosts[i].Measured = false
				}
				result.Hosts[i].Applied = id
			}
		}
		result.Boxes = adaptiveBoxes(result.Hosts, result.History, c, time.Now().Unix())
	}()
	f, err := os.Open(filepath.Join(c.RuntimeDir, "autohosts.txt"))
	if err != nil {
		if !os.IsNotExist(err) {
			result.Error = err.Error()
		}
		return result
	}
	defer f.Close()
	scanner := bufio.NewScanner(io.LimitReader(f, 262144))
	seen := map[string]bool{}
	for scanner.Scan() {
		host := strings.TrimSpace(scanner.Text())
		if ValidDomain(host) && !seen[host] {
			seen[host] = true
			result.Discovered = append(result.Discovered, host)
			if len(result.Discovered) >= 1000 {
				break
			}
		}
	}
	if err = scanner.Err(); err != nil {
		result.Error = err.Error()
	}
	return result
}

// Boxes are groups of compatible observations, not identified physical appliances.
type AdaptiveMethod struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Family   string `json:"family"`
	Hosts    int    `json:"hosts"`
	Latest   int64  `json:"latest"`
	Reusable bool   `json:"reusable"`
}
type AdaptiveBox struct {
	ID      string           `json:"id"`
	TLS     string           `json:"tls"`
	Family  string           `json:"family"`
	Network string           `json:"network"`
	Port    int              `json:"port"`
	Symptom string           `json:"symptom"`
	Updated int64            `json:"updated"`
	Hosts   []AdaptiveHost   `json:"hosts"`
	Methods []AdaptiveMethod `json:"methods"`
	History []AdaptiveHost   `json:"history"`
}

func observationProtocol(group string) (tls, family string) {
	tls, family = "unknown", "unknown"
	for _, v := range []string{"tls12", "tls13", "legacy", "unknown"} {
		for _, f := range []string{"ipv4", "ipv6"} {
			if strings.HasSuffix(group, "_"+v+"_"+f) { return v, f }
		}
	}
	return
}
func boxContext(r AdaptiveHost) AdaptiveBox {
	tls, family := observationProtocol(r.Group)
	key := fmt.Sprintf("%s/%s/%s/%d/%s", tls, family, r.Network, r.Port, r.Symptom)
	sum := sha256.Sum256([]byte(key))
	return AdaptiveBox{ID: fmt.Sprintf("box-%x", sum[:8]), TLS: tls, Family: family, Network: r.Network, Port: r.Port, Symptom: r.Symptom, Hosts: []AdaptiveHost{}, Methods: []AdaptiveMethod{}, History: []AdaptiveHost{}}
}
func adaptiveBoxes(hosts, events []AdaptiveHost, c Config, now int64) []AdaptiveBox {
	boxes := map[string]*AdaptiveBox{}
	get := func(r AdaptiveHost) *AdaptiveBox {
		b := boxContext(r)
		if old, ok := boxes[b.ID]; ok {
			return old
		}
		boxes[b.ID] = &b
		return &b
	}
	// A host can retain older records after profile edits. Use its newest record
	// for each TLS/IP/route/port context so an old success cannot hide a new failure.
	latest := map[string]AdaptiveHost{}
	for _, r := range hosts {
		b := boxContext(r)
		key := fmt.Sprintf("%s/%s/%s/%d/%s", r.Host, b.TLS, b.Family, b.Port, b.Network)
		old, ok := latest[key]
		if !ok || r.Updated > old.Updated || (r.Updated == old.Updated && (r.Status == "probing" && old.Status != "probing" || r.Status == old.Status && r.Group < old.Group)) {
			latest[key] = r
		}
	}
	for _, r := range latest {
		b := get(r)
		b.Hosts = append(b.Hosts, r)
		if r.Updated > b.Updated {
			b.Updated = r.Updated
		}
	}
	for _, r := range events {
		b := get(r)
		b.History = append(b.History, r)
		if r.Updated > b.Updated {
			b.Updated = r.Updated
		}
	}
	result := []AdaptiveBox{}
	for _, b := range boxes {
		sort.Slice(b.Hosts, func(i, j int) bool { return b.Hosts[i].Host < b.Hosts[j].Host })
		methods := map[string]*AdaptiveMethod{}
		for _, r := range b.Hosts {
			if r.Status != "connected" || r.Strategy == "none" {
				continue
			}
			strategy, ok := strategyFor(c, r.Strategy)
			if !ok {
				continue
			}
			m := methods[r.Strategy]
			if m == nil {
				m = &AdaptiveMethod{ID: r.Strategy, Name: strategy.Name, Family: StrategyFamily(strategy)}
				methods[r.Strategy] = m
			}
			m.Hosts++
			if r.Updated > m.Latest {
				m.Latest = r.Updated
			}
			if !r.Paused && r.Pinned == "" && r.Origin != "manual" && b.TLS != "unknown" && b.TLS != "legacy" && b.Family != "unknown" && b.Symptom != "unknown" && b.Network == c.WAN && b.Network != "-" && b.Port > 0 && now >= r.Updated && now-r.Updated <= 7*24*60*60 {
				m.Reusable = true
			}
		}
		for _, m := range methods {
			b.Methods = append(b.Methods, *m)
		}
		sort.Slice(b.Methods, func(i, j int) bool {
			a, z := b.Methods[i], b.Methods[j]
			if a.Hosts != z.Hosts {
				return a.Hosts > z.Hosts
			}
			if a.Latest != z.Latest {
				return a.Latest > z.Latest
			}
			return a.ID < z.ID
		})
		result = append(result, *b)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Updated != result[j].Updated {
			return result[i].Updated > result[j].Updated
		}
		return result[i].ID < result[j].ID
	})
	return result
}
