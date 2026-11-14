package pilot

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

func adaptiveFrame(generation uint64, hosts, events []AdaptiveHost) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "PILOT3\t%d\n", generation)
	row := func(kind string, r AdaptiveHost) {
		hold := r.HoldReason
		if hold == "" {
			hold = "none"
		}
		fmt.Fprintf(&b, "%s\t%s\t%s\t%s\t%s\t%d\t%s\t%s\t%d\t%s\t%d\t%d\t%d\t%d\t%s\n", kind, r.Group, r.Host, r.Strategy, r.Status, r.Updated, r.Symptom, r.Network, r.Port, r.Origin, r.LastSwitch, r.WindowStart, r.Switches, r.HoldUntil, hold)
	}
	for _, r := range hosts {
		row("R", r)
	}
	for _, r := range events {
		row("E", r)
	}
	fmt.Fprintf(&b, "END\t%d\n", generation)
	return b.Bytes()
}

// Only called while the controller's operation lock is held and the packet
// engine is stopped. Atomic replacement retains its unprivileged ownership.
func writeAdaptiveFile(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".pilot-state-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0600); err == nil && os.Geteuid() == 0 {
		err = f.Chown(adaptiveUID, adaptiveUID)
	}
	if err == nil {
		_, err = f.Write(data)
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

func (m *Manager) resetDomainLocked(c Config, host string) (result error) {
	if _, err := os.Lstat(c.RuntimeDir); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	if err := PrepareAdaptive(c); err != nil {
		return err
	}
	running := m.backend.Running()
	backups := map[string][]byte{}
	modified := false
	defer func() {
		if result != nil {
			if modified {
				for name, data := range backups {
					result = errors.Join(result, writeAdaptiveFile(filepath.Join(c.RuntimeDir, name), data))
				}
			}
			if running {
				result = errors.Join(result, m.backend.Start(c))
			}
		}
		m.setError(result)
	}()
	if running {
		if err := m.backend.Stop(); err != nil {
			return err
		}
	}
	// Snapshot only after stopping the writer. Reads and writes stay bounded.
	for _, name := range []string{"state0.tsv", "state1.tsv", "autohosts.txt"} {
		f, err := os.Open(filepath.Join(c.RuntimeDir, name))
		if err != nil {
			return err
		}
		data, err := io.ReadAll(io.LimitReader(f, 524289))
		f.Close()
		if err != nil {
			return err
		}
		if len(data) > 524288 {
			return errors.New("слишком большой файл опыта; сброс не выполнен")
		}
		backups[name] = data
	}
	snapshot := ReadAdaptive(c)
	if snapshot.Generation == 0 && (len(backups["state0.tsv"]) > 0 || len(backups["state1.tsv"]) > 0) {
		return errors.New("нет завершённого кадра опыта; отказ стирать остальные наблюдения")
	}
	if snapshot.Generation >= (1<<53)-2 {
		return errors.New("исчерпан счётчик кадров опыта")
	}
	hosts := slices.DeleteFunc(snapshot.Hosts, func(r AdaptiveHost) bool { return r.Host == host })
	events := slices.DeleteFunc(snapshot.History, func(r AdaptiveHost) bool { return r.Host == host })
	discovered := []string{}
	for _, line := range strings.Split(string(backups["autohosts.txt"]), "\n") {
		if ValidDomain(line) && line != host && !slices.Contains(discovered, line) && len(discovered) < 1000 {
			discovered = append(discovered, line)
		}
	}
	files := map[string][]byte{
		"state0.tsv":    adaptiveFrame(snapshot.Generation+1, hosts, events),
		"state1.tsv":    adaptiveFrame(snapshot.Generation+2, hosts, events),
		"autohosts.txt": []byte(strings.Join(discovered, "\n")),
	}
	if len(discovered) > 0 {
		files["autohosts.txt"] = append(files["autohosts.txt"], '\n')
	}
	modified = true
	for _, name := range []string{"state0.tsv", "state1.tsv", "autohosts.txt"} {
		if err := writeAdaptiveFile(filepath.Join(c.RuntimeDir, name), files[name]); err != nil {
			return err
		}
	}
	if running {
		if err := m.backend.Start(c); err != nil {
			return err
		}
	}
	m.log.Add("Обучение домена сброшено: " + host + "; ручные правила сохранены")
	return nil
}
