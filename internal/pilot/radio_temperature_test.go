package pilot

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestKeeneticRadioTemperatureAndBoundedCache(t *testing.T) {
	r := radioReader{}
	calls := 0
	run := func(radio string) ([]byte, error) {
		calls++
		v := "70"
		if radio == "WifiMaster1" {
			v = "59"
		}
		return []byte("id: " + radio + "\n type: WifiMaster\n channel: 10\n temperature: " + v + "\n summary:\n layer: running\n"), nil
	}
	now := time.Unix(1000, 0)
	first := r.Read(now, run)
	if len(first) != 2 || first[0].Celsius != 70 || first[1].Celsius != 59 {
		t.Fatal("radio units or readings wrong", first)
	}
	first[0].Celsius = 0
	if cached := r.Read(now.Add(5*time.Second), run); calls != 2 || cached[0].Celsius != 70 {
		t.Fatal("radio cache changed or polled too often", calls, cached)
	}
	r.Read(now.Add(31*time.Second), run)
	if calls != 4 {
		t.Fatal("radio cache never refreshed", calls)
	}
	missing := r.Read(now.Add(62*time.Second), func(string) ([]byte, error) { return nil, errors.New("unavailable") })
	if len(missing) != 0 {
		t.Fatal("failed read reused old temperatures", missing)
	}
	for _, data := range []string{"id: WifiMaster1\ntype: WifiMaster\ntemperature: 70", "id: WifiMaster0\ntemperature: 70", "id: WifiMaster0\ntype: WifiMaster\ntemperature: NaN", "id: WifiMaster0\ntype: WifiMaster\ntemperature: 70000"} {
		if _, ok := parseRadioTemperature([]byte(data), "WifiMaster0"); ok {
			t.Fatal("invalid radio accepted", data)
		}
	}
}

func TestKeeneticRadioCommandUsesFixedReadOnlyArguments(t *testing.T) {
	dir := t.TempDir()
	script := "#!/bin/sh\n[ \"$#\" = 2 ] && [ \"$1\" = -c ] && [ \"$2\" = 'show interface WifiMaster0' ] || exit 91\nprintf 'id: WifiMaster0\\ntype: WifiMaster\\ntemperature: 70\\n'\n"
	if err := os.WriteFile(filepath.Join(dir, "ndmc"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	data, err := readRadioCommand("WifiMaster0")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := parseRadioTemperature(data, "WifiMaster0"); !ok {
		t.Fatal("command result unreadable", string(data))
	}
	if _, err := readRadioCommand("WifiMaster0; reboot"); err == nil {
		t.Fatal("arbitrary CLI input accepted")
	}
	var bounded radioOutput
	_, _ = bounded.Write([]byte(strings.Repeat("x", 20000)))
	if bounded.data.Len() != 16384 || !bounded.overflow {
		t.Fatal("unbounded CLI output")
	}
}
