package pilot

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDeviceSamplesAndMissingSensors(t *testing.T) {
	root := t.TempDir()
	put := func(p, v string) {
		t.Helper()
		path := filepath.Join(root, p)
		os.MkdirAll(filepath.Dir(path), 0700)
		if e := os.WriteFile(path, []byte(v), 0600); e != nil {
			t.Fatal(e)
		}
	}
	put("proc/device-tree/model", "KN-1811 fixture\x00")
	put("proc/stat", "cpu 100 0 30 200 10 0 0 0\n")
	put("proc/loadavg", "0.25 0.50 0.75 1/100 99")
	put("proc/uptime", "90000.5 0")
	put("proc/meminfo", "MemTotal: 1000 kB\nMemAvailable: 400 kB\n")
	put("proc/net/dev", "ppp0: 1000 0 0 0 0 0 0 0 500 0 0 0 0 0 0 0\n")
	put("sys/class/thermal/thermal_zone0/temp", "52000")
	put("sys/class/thermal/thermal_zone0/type", "cpu")
	put("sys/class/hwmon/hwmon0/temp1_input", "NaN")
	r := deviceReader{}
	now := time.Unix(1000, 0)
	a := r.Read(root, "ppp0", now)
	if a.Model != "KN-1811 fixture" || a.CPUPercent != nil || !a.CPUSampleAvailable || a.MemoryAvailable == nil || *a.MemoryAvailable != 400*1024 || len(a.Temperatures) != 1 || a.Temperatures[0].Celsius != 52 || a.RXRate != nil {
		t.Fatalf("first sample wrong: %+v", a)
	}
	put("proc/stat", "cpu 150 0 40 240 10 0 0 0\n")
	put("proc/net/dev", "ppp0: 1400 0 0 0 0 0 0 0 700 0 0 0 0 0 0 0\n")
	b := r.Read(root, "ppp0", now.Add(4*time.Second))
	if b.CPUPercent == nil || *b.CPUPercent < 59.9 || *b.CPUPercent > 60.1 || b.RXRate == nil || *b.RXRate != 100 || b.TXRate == nil || *b.TXRate != 50 {
		t.Fatalf("delta sample wrong: %+v", b)
	}
	if _, e := json.Marshal(b); e != nil {
		t.Fatal("invalid metric JSON", e)
	}
	missing := r.Read(t.TempDir(), "unknown", now.Add(8*time.Second))
	if missing.CPUSampleAvailable || missing.CPUPercent != nil || missing.MemoryAvailable != nil || missing.RXBytes != nil || len(missing.Temperatures) != 0 {
		t.Fatal("missing metrics fabricated")
	}
}

func TestWiFiHwmonNamesAndDuplicatePaths(t *testing.T) {
	root := t.TempDir()
	hwmon := filepath.Join(root, "sys/class/hwmon/hwmon0")
	if err := os.MkdirAll(hwmon, 0700); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{"name": "mt7915", "temp1_input": "61000", "temp1_label": "radio 2.4 GHz", "temp2_input": "65000", "temp2_label": "radio 5 GHz"} {
		if err := os.WriteFile(filepath.Join(hwmon, name), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	radio := filepath.Join(root, "sys/class/ieee80211/phy0/device/hwmon")
	if err := os.MkdirAll(radio, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(hwmon, filepath.Join(radio, "hwmon0")); err != nil {
		t.Fatal(err)
	}
	r := deviceReader{}
	d := r.Read(root, "", time.Now())
	if len(d.Temperatures) != 2 {
		t.Fatalf("duplicate radio sensors: %+v", d.Temperatures)
	}
	if d.Temperatures[0].Name != "Wi-Fi phy0 · mt7915 · radio 2.4 GHz" || d.Temperatures[0].Celsius != 61 || d.Temperatures[1].Name != "Wi-Fi phy0 · mt7915 · radio 5 GHz" || d.Temperatures[1].Celsius != 65 {
		t.Fatalf("radio names or units: %+v", d.Temperatures)
	}
}
