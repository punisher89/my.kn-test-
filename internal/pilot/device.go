package pilot

import (
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Temperature struct {
	Name    string  `json:"name"`
	Celsius float64 `json:"celsius"`
}
type DeviceInfo struct {
	Model              string        `json:"model"`
	Platform           string        `json:"platform"`
	Kernel             string        `json:"kernel"`
	CPUSampleAvailable bool          `json:"cpu_sample_available"`
	CPUCores           int           `json:"cpu_cores"`
	CPUPercent         *float64      `json:"cpu_percent"`
	PilotCPUPercent *float64 `json:"pilot_cpu_percent,omitempty"`
	EngineCPUPercent *float64 `json:"engine_cpu_percent,omitempty"`
	Load               []float64     `json:"load"`
	MemoryTotal        uint64        `json:"memory_total"`
	MemoryAvailable    *uint64       `json:"memory_available"`
	Uptime             *float64      `json:"uptime_seconds"`
	Temperatures       []Temperature `json:"temperatures"`
	WAN                string        `json:"wan"`
	RXBytes            *uint64       `json:"rx_bytes"`
	TXBytes            *uint64       `json:"tx_bytes"`
	RXRate             *float64      `json:"rx_bytes_per_second"`
	TXRate             *float64      `json:"tx_bytes_per_second"`
}
type deviceReader struct {
	mu                  sync.Mutex
	total, idle, rx, tx uint64
	cpuValid, netValid  bool
	wan                 string
	at                  time.Time
	resourceAt          time.Time
	resourceRoot        string
	resourceSample      DeviceInfo
}

func smallRead(root, path string) string {
	b, err := os.ReadFile(filepath.Join(root, path))
	if err != nil || len(b) > 1<<20 {
		return ""
	}
	return strings.TrimSpace(strings.Trim(string(b), "\x00"))
}
func (r *deviceReader) Read(root, wan string, now time.Time) DeviceInfo {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.read(root, wan, now, false)
}

// The guard needs a stable CPU interval and memory/load, not repeated sysfs
// temperature and network scans between two back-to-back trial connections.
func (r *deviceReader) ReadResources(root string, now time.Time) DeviceInfo {
	r.mu.Lock()
	defer r.mu.Unlock()
	if root == r.resourceRoot && !r.resourceAt.IsZero() && !now.Before(r.resourceAt) && now.Sub(r.resourceAt) < 2*time.Second {
		return r.resourceSample
	}
	d := r.read(root, "", now, true)
	r.resourceAt, r.resourceRoot, r.resourceSample = now, root, d
	return d
}

func (r *deviceReader) read(root, wan string, now time.Time, resourcesOnly bool) DeviceInfo {
	d := DeviceInfo{Platform: runtime.GOOS + "/" + runtime.GOARCH, CPUCores: runtime.NumCPU(), WAN: wan, Load: []float64{}, Temperatures: []Temperature{}}
	if !resourcesOnly {
		d.Model = smallRead(root, "/proc/device-tree/model")
		d.Kernel = smallRead(root, "/proc/sys/kernel/osrelease")
		if d.Model == "" {
			d.Model = smallRead(root, "/sys/firmware/devicetree/base/model")
		}
		for _, line := range strings.Split(smallRead(root, "/proc/cpuinfo"), "\n") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 && d.Model == "" && (strings.TrimSpace(parts[0]) == "Hardware" || strings.TrimSpace(parts[0]) == "system type") {
				d.Model = strings.TrimSpace(parts[1])
			}
		}
	}
	cpu := strings.Fields(strings.Split(smallRead(root, "/proc/stat"), "\n")[0])
	var total, idle uint64
	valid := len(cpu) >= 5 && cpu[0] == "cpu"
	if valid {
		for i := 1; i < len(cpu) && i <= 8; i++ {
			v, e := strconv.ParseUint(cpu[i], 10, 64)
			if e != nil {
				valid = false
				break
			}
			total += v
			if i == 4 || i == 5 {
				idle += v
			}
		}
	}
	if valid && r.cpuValid && total > r.total && idle >= r.idle && idle-r.idle <= total-r.total {
		v := 100 * (1 - float64(idle-r.idle)/float64(total-r.total))
		d.CPUPercent = &v
	}
	d.CPUSampleAvailable = valid
	r.total, r.idle, r.cpuValid = total, idle, valid
	fields := strings.Fields(smallRead(root, "/proc/loadavg"))
	if len(fields) >= 3 {
		for _, s := range fields[:3] {
			v, e := strconv.ParseFloat(s, 64)
			if e == nil && v >= 0 && !math.IsInf(v, 0) && !math.IsNaN(v) {
				d.Load = append(d.Load, v)
			}
		}
	}
	if f := strings.Fields(smallRead(root, "/proc/uptime")); len(f) > 0 {
		if v, e := strconv.ParseFloat(f[0], 64); e == nil && v >= 0 && !math.IsInf(v, 0) && !math.IsNaN(v) {
			d.Uptime = &v
		}
	}
	mem := map[string]uint64{}
	for _, line := range strings.Split(smallRead(root, "/proc/meminfo"), "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 {
			if v, e := strconv.ParseUint(f[1], 10, 64); e == nil {
				mem[strings.TrimSuffix(f[0], ":")] = v * 1024
			}
		}
	}
	d.MemoryTotal = mem["MemTotal"]
	_, hasAvailable := mem["MemAvailable"]
	_, hasFree := mem["MemFree"]
	if d.MemoryTotal > 0 && (hasAvailable || hasFree) {
		v, ok := mem["MemAvailable"]
		if !ok {
			v = mem["MemFree"] + mem["Buffers"] + mem["Cached"] + mem["SReclaimable"]
			if v >= mem["Shmem"] {
				v -= mem["Shmem"]
			}
		}
		if v > d.MemoryTotal {
			v = d.MemoryTotal
		}
		d.MemoryAvailable = &v
	}
	if resourcesOnly {
		return d
	}
	thermal, _ := filepath.Glob(filepath.Join(root, "sys/class/thermal/thermal_zone*/temp"))
	hwmon, _ := filepath.Glob(filepath.Join(root, "sys/class/hwmon/hwmon*/temp*_input"))
	radioNames := map[string]string{}
	for _, pattern := range []string{"sys/class/ieee80211/phy*/device/hwmon/hwmon*/temp*_input", "sys/class/ieee80211/phy*/hwmon*/temp*_input"} {
		paths, _ := filepath.Glob(filepath.Join(root, pattern))
		for _, path := range paths {
			resolved, err := filepath.EvalSymlinks(path)
			if err != nil {
				continue
			}
			for _, part := range strings.Split(path, string(filepath.Separator)) {
				if strings.HasPrefix(part, "phy") {
					radioNames[resolved] = "Wi-Fi " + part
					break
				}
			}
			hwmon = append(hwmon, path)
		}
	}
	seenSensors := map[string]bool{}
	for _, path := range append(thermal, hwmon...) {
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil || seenSensors[resolved] {
			continue
		}
		seenSensors[resolved] = true
		b, e := os.ReadFile(path)
		if e != nil || len(b) > 64 {
			continue
		}
		v, e := strconv.ParseFloat(strings.TrimSpace(string(b)), 64)
		if e != nil || math.IsNaN(v) || math.IsInf(v, 0) {
			continue
		}
		// Linux thermal/hwmon ABI exposes millidegrees Celsius.
		v /= 1000
		if v < -30 || v > 150 {
			continue
		}
		name := smallRead("", filepath.Join(filepath.Dir(path), "type"))
		// hwmon names identify radio chips (mt7915, mt76, ath10k...) even
		// when the driver provides no tempN_label. Keep the channel distinct.
		chip := smallRead("", filepath.Join(filepath.Dir(path), "name"))
		if name == "" {
			name = smallRead("", strings.TrimSuffix(path, "_input")+"_label")
			if chip != "" {
				if name == "" {
					name = strings.TrimSuffix(filepath.Base(path), "_input")
				}
				name = chip + " · " + name
			}
		}
		if name == "" {
			name = filepath.Base(filepath.Dir(path)) + "/" + filepath.Base(path)
		}
		if radio := radioNames[resolved]; radio != "" {
			name = radio + " · " + name
		}
		d.Temperatures = append(d.Temperatures, Temperature{Name: name, Celsius: v})
	}
	for _, line := range strings.Split(smallRead(root, "/proc/net/dev"), "\n") {
		p := strings.SplitN(line, ":", 2)
		if len(p) != 2 || strings.TrimSpace(p[0]) != wan {
			continue
		}
		f := strings.Fields(p[1])
		if len(f) < 16 {
			continue
		}
		rx, e1 := strconv.ParseUint(f[0], 10, 64)
		tx, e2 := strconv.ParseUint(f[8], 10, 64)
		if e1 != nil || e2 != nil {
			continue
		}
		d.RXBytes = &rx
		d.TXBytes = &tx
		if r.netValid && r.wan == wan && now.After(r.at) && rx >= r.rx && tx >= r.tx {
			dt := now.Sub(r.at).Seconds()
			a, b := float64(rx-r.rx)/dt, float64(tx-r.tx)/dt
			d.RXRate = &a
			d.TXRate = &b
		}
		r.rx, r.tx = rx, tx
	}
	r.netValid = d.RXBytes != nil
	r.wan = wan
	r.at = now
	return d
}
func (m *Manager) Device() DeviceInfo {
	now := time.Now()
	d := m.device.Read("", m.Config().WAN, now)
    var enginePID int
    if router,ok:=m.backend.(*Router);ok {
       router.mu.Lock()
       if router.cmd!=nil&&router.cmd.Process!=nil {enginePID=router.cmd.Process.Pid}
       router.mu.Unlock()
    }
    d.PilotCPUPercent,d.EngineCPUPercent=m.processCPU.sample("",os.Getpid(),enginePID,now)
    if m.backend.Kind() == "router" {
		d.Temperatures = append(d.Temperatures, m.radio.Read(now, readRadioCommand)...)
	}
	return d
}
