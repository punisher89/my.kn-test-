package pilot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

// LockInstance must be held throughout the daemon lifetime. In particular, a
// second daemon must never unlink the first daemon's socket or firewall journal.
func LockInstance(dir string) (*os.File, error) {
	f, err := os.OpenFile(filepath.Join(dir, "daemon.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("экземпляр уже запущен: %w", err)
	}
	return f, nil
}

func ControlListener(dir string) (net.Listener, error) {
	path := filepath.Join(dir, "control.sock")
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("%s существует и не является сокетом", path)
		}
		if err = os.Remove(path); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	l, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err = os.Chmod(path, 0600); err != nil {
		l.Close()
		return nil, err
	}
	return l, nil
}

func (m *Manager) Repair() error {
	if !m.op.TryLock() {
		return ErrBusy
	}
	defer m.op.Unlock()
	if !m.Config().Enabled {
		return nil
	}
	if !m.backend.Running() {
		return m.setError(m.backend.Start(m.Config()))
	}
	return m.setError(m.backend.Repair())
}

func ControlHandler(m *Manager) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(m.Snapshot())
	})
	mux.HandleFunc("POST /repair", func(w http.ResponseWriter, r *http.Request) {
		if err := m.Repair(); err != nil {
			code := 500
			if errors.Is(err, ErrBusy) {
				code = 409
			}
			http.Error(w, err.Error(), code)
			return
		}
		w.WriteHeader(204)
	})
	return mux
}

func ControlRequest(ctx context.Context, dir, method, path string) ([]byte, error) {
	dialer := &net.Dialer{}
	t := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return dialer.DialContext(ctx, "unix", filepath.Join(dir, "control.sock"))
	}}
	defer t.CloseIdleConnections()
	req, err := http.NewRequestWithContext(ctx, method, "http://pilot"+path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := (&http.Client{Transport: t}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("control HTTP %d: %s", resp.StatusCode, body)
	}
	return body, nil
}

type DoctorCheck struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

// Diagnose is read-only. It never adds rules, loads kernel modules, or prints
// token contents. Actual NFQUEUE/PPE behaviour still needs a router test.
func Diagnose(c Config) []DoctorCheck {
	r := []DoctorCheck{}
	add := func(name string, err error, okDetail string) {
		v := DoctorCheck{Name: name, OK: err == nil, Detail: okDetail}
		if err != nil {
			v.Detail = err.Error()
		}
		r = append(r, v)
	}
	add("config", c.Validate(), "конфигурация корректна")
	var rootErr error
	if os.Geteuid() != 0 {
		rootErr = errors.New("для обработки пакетов нужен root")
	}
	add("root", rootErr, "root")
	_, wanErr := os.Stat(filepath.Join("/sys/class/net", c.WAN))
	if c.WAN == "" {
		wanErr = errors.New("WAN не задан")
	}
	add("WAN", wanErr, c.WAN)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, filepath.Join(c.EngineDir, "nfqws2"), append([]string{"--dry-run"}, EngineArgs(c)...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		err = fmt.Errorf("%w: %s", err, tail(out, 1200))
	}
	add("nfqws2", err, "параметры и Lua загружены; перехват не запускался")
	tools := []string{"iptables"}
	if c.IPv6 {
		tools = append(tools, "ip6tables")
	}
	for _, tool := range tools {
		add(tool, RunCommand(tool, "-w", "5", "-t", "mangle", "-S"), "таблица mangle доступна")
	}
	return r
}
