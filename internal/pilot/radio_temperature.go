package pilot

import (
	"bytes"
	"context"
	"errors"
	"math"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type radioReader struct {
	mu           sync.Mutex
	at           time.Time
	temperatures []Temperature
}

type radioOutput struct {
	data     bytes.Buffer
	overflow bool
}

func (b *radioOutput) Write(p []byte) (int, error) {
	n := len(p)
	remaining := 16384 - b.data.Len()
	if len(p) > remaining {
		b.overflow = true
		p = p[:remaining]
	}
	_, _ = b.data.Write(p)
	return n, nil
}

// These are fixed read-only commands. Never accept a command or interface
// supplied by the browser, and never export the rest of the interface output.
func readRadioCommand(radio string) ([]byte, error) {
	if radio != "WifiMaster0" && radio != "WifiMaster1" {
		return nil, errors.New("unsupported radio")
	}
	bin, err := exec.LookPath("ndmc")
	if err != nil || !filepath.IsAbs(bin) {
		return nil, errors.New("ndmc unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "-c", "show interface "+radio)
	cmd.WaitDelay = 250 * time.Millisecond
	var output radioOutput
	cmd.Stdout = &output
	if err = cmd.Run(); err != nil {
		return nil, err
	}
	if output.overflow {
		return nil, errors.New("radio output too large")
	}
	return output.data.Bytes(), nil
}

func parseRadioTemperature(data []byte, radio string) (Temperature, bool) {
	if len(data) > 16384 {
		return Temperature{}, false
	}
	id, kind, value := "", "", ""
	for _, line := range strings.Split(string(data), "\n") {
		parts := strings.SplitN(strings.TrimSpace(line), ":", 2)
		if len(parts) != 2 {
			continue
		}
		switch parts[0] {
		case "id":
			id = strings.TrimSpace(parts[1])
		case "type":
			kind = strings.TrimSpace(parts[1])
		case "temperature":
			if value != "" {
				return Temperature{}, false
			}
			value = strings.TrimSpace(parts[1])
		}
	}
	v, err := strconv.ParseFloat(value, 64)
	if id != radio || kind != "WifiMaster" || err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < -30 || v > 150 {
		return Temperature{}, false
	}
	return Temperature{Name: "Wi-Fi " + radio + " · Keenetic", Celsius: v}, true
}

func (r *radioReader) Read(now time.Time, run func(string) ([]byte, error)) []Temperature {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.at.IsZero() || now.Before(r.at) || now.Sub(r.at) >= 30*time.Second {
		r.temperatures = []Temperature{}
		for _, radio := range []string{"WifiMaster0", "WifiMaster1"} {
			if data, err := run(radio); err == nil {
				if t, ok := parseRadioTemperature(data, radio); ok {
					r.temperatures = append(r.temperatures, t)
				}
			}
		}
		r.at = now
	}
	return append([]Temperature{}, r.temperatures...)
}
