package pilot

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
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
	for _, name := range []string{"autohosts.txt", "state0.tsv", "state1.tsv"} {
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
	return nil
}

type AdaptiveHost struct {
	Group    string `json:"group"`
	Host     string `json:"host"`
	Strategy string `json:"strategy"`
	Status   string `json:"status"`
	Updated  int64  `json:"updated"`
}
type AdaptiveSnapshot struct {
	Enabled    bool           `json:"enabled"`
	Generation uint64         `json:"generation"`
	Hosts      []AdaptiveHost `json:"hosts"`
	Discovered []string       `json:"discovered"`
	Error      string         `json:"error,omitempty"`
}

var groupRE = regexp.MustCompile(`^[a-z0-9_-]{1,96}$`)

func readAdaptiveSlot(path string) (uint64, []AdaptiveHost, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 262145))
	if err != nil {
		return 0, nil, err
	}
	if len(data) > 262144 {
		return 0, nil, errors.New("слишком большой файл наблюдений")
	}
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	if !scanner.Scan() {
		return 0, nil, io.ErrUnexpectedEOF
	}
	header := strings.Split(scanner.Text(), "\t")
	if len(header) != 2 || header[0] != "PILOT1" {
		return 0, nil, errors.New("неполный заголовок adaptive")
	}
	generation, err := strconv.ParseUint(header[1], 10, 53)
	if err != nil {
		return 0, nil, err
	}
	hosts := []AdaptiveHost{}
	seen := map[string]bool{}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "END\t"+header[1] {
			if scanner.Scan() {
				return 0, nil, errors.New("лишние данные adaptive")
			}
			return generation, hosts, scanner.Err()
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 5 || len(hosts) >= 512 {
			return 0, nil, errors.New("неполная запись adaptive")
		}
		stamp, err := strconv.ParseInt(fields[4], 10, 64)
		if err != nil || stamp < 0 || !groupRE.MatchString(fields[0]) || !ValidDomain(fields[1]) || !idRE.MatchString(fields[2]) || (fields[3] != "probing" && fields[3] != "connected") {
			return 0, nil, errors.New("некорректная запись adaptive")
		}
		key := fields[0] + "\t" + fields[1]
		if seen[key] {
			return 0, nil, errors.New("повтор записи adaptive")
		}
		seen[key] = true
		hosts = append(hosts, AdaptiveHost{Group: fields[0], Host: fields[1], Strategy: fields[2], Status: fields[3], Updated: stamp})
	}
	return 0, nil, io.ErrUnexpectedEOF
}

func ReadAdaptive(c Config) AdaptiveSnapshot {
	result := AdaptiveSnapshot{Enabled: c.Adaptive, Hosts: []AdaptiveHost{}, Discovered: []string{}}
	for i := 0; i < 2; i++ {
		n, hosts, err := readAdaptiveSlot(filepath.Join(c.RuntimeDir, fmt.Sprintf("state%d.tsv", i)))
		if err == nil && n > result.Generation {
			result.Generation = n
			result.Hosts = hosts
		}
	}
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
