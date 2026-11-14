package pilot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

type Backend interface {
	Start(Config) error
	Stop() error
	Repair() error
	Running() bool
	Kind() string
}

type LogBuffer struct {
	mu    sync.Mutex
	lines []string
}

func (b *LogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, line := range strings.Split(strings.TrimSpace(string(p)), "\n") {
		if line != "" {
			if len(line) > 1000 {
				line = line[:1000]
			}
			b.lines = append(b.lines, time.Now().UTC().Format(time.RFC3339)+" "+line)
		}
	}
	if len(b.lines) > 200 {
		b.lines = b.lines[len(b.lines)-200:]
	}
	return len(p), nil
}
func (b *LogBuffer) Add(s string) { _, _ = b.Write([]byte(s)) }
func (b *LogBuffer) Lines() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string{}, b.lines...)
}

type Router struct {
	mu      sync.Mutex
	cmd     *exec.Cmd
	done    chan error
	rules   []Rule
	journal string
	log     *LogBuffer
	liberal []byte
	acct    []byte
}

type firewallJournal struct {
	Rules   []Rule `json:"rules"`
	Liberal []byte `json:"original_tcp_be_liberal,omitempty"`
	Acct    []byte `json:"original_conntrack_acct,omitempty"`
}

func NewRouter(stateDir string, log *LogBuffer) *Router {
	return &Router{journal: filepath.Join(stateDir, "firewall.json"), log: log}
}
func (r *Router) Kind() string  { return "router" }
func (r *Router) Running() bool { r.mu.Lock(); defer r.mu.Unlock(); return r.running() }
func (r *Router) running() bool {
	if r.cmd == nil {
		return false
	}
	select {
	case err := <-r.done:
		if err != nil {
			r.log.Add("nfqws2 завершился: " + err.Error())
		}
		r.cmd = nil
		return false
	default:
		return true
	}
}
func (r *Router) Repair() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.running() {
		return errors.New("nfqws2 не запущен")
	}
	return EnsureRules(RunCommand, r.rules)
}

func (r *Router) Start(c Config) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := c.Validate(); err != nil {
		return err
	}
	if c.WAN == "" {
		return errors.New("укажите WAN-интерфейс в настройках")
	}
	if os.Geteuid() != 0 {
		return errors.New("работа NFQUEUE требует root на роутере")
	}
	if _, err := os.Stat(filepath.Join("/sys/class/net", c.WAN)); err != nil {
		return fmt.Errorf("WAN %s не найден: %w", c.WAN, err)
	}
	binary := filepath.Join(c.EngineDir, "nfqws2")
	args := EngineArgs(c)
	if err := CheckEngine(c); err != nil {
		return err
	}
	if err := r.stop(); err != nil {
		return err
	}
	var err error
	r.liberal, r.acct, err = readConntrackSettings(conntrackSysctlDir)
	if err != nil {
		return err
	}
	if len(r.acct) == 0 {
		r.log.Add("nf_conntrack_acct не предоставлен прошивкой: sysctl не меняется; ограничение connbytes сохраняется")
	}
	if len(r.liberal) == 0 {
		r.log.Add("nf_conntrack_tcp_be_liberal не предоставлен прошивкой: sysctl не меняется")
	}
	r.rules = FirewallRules(c)
	// Journal original settings BEFORE mutating them, so a crash can be cleaned
	// even when the next daemon starts with packet interception disabled.
	if err := WriteJSON(r.journal, firewallJournal{Rules: r.rules, Liberal: r.liberal, Acct: r.acct}); err != nil {
		r.rules = nil
		r.liberal = nil
		r.acct = nil
		return err
	}
	if err := enableConntrackSettings(conntrackSysctlDir, r.liberal, r.acct); err != nil {
		return errors.Join(err, r.stop())
	}
	r.cmd = exec.Command(binary, args...)
	r.cmd.Stdout = r.log
	r.cmd.Stderr = r.log
	r.cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM}
	if err := r.cmd.Start(); err != nil {
		r.cmd = nil
		return errors.Join(err, r.stop())
	}
	cmd := r.cmd
	r.done = make(chan error, 1)
	done := r.done
	go func() { done <- cmd.Wait(); close(done) }()
	select {
	case err := <-r.done:
		r.cmd = nil
		return errors.Join(fmt.Errorf("nfqws2 завершился при запуске: %v", err), r.stop())
	case <-time.After(350 * time.Millisecond):
	}
	if err := EnsureRules(RunCommand, r.rules); err != nil {
		cleanup := r.stop()
		return errors.Join(err, cleanup)
	}
	r.log.Add("Обход запущен: WAN=" + c.WAN)
	return nil
}

func CheckEngine(c Config) error {
	if err := c.Validate(); err != nil {
		return err
	}
	if c.Adaptive && !c.tuning {
		if err := PrepareAdaptive(c); err != nil {
			return err
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, filepath.Join(c.EngineDir, "nfqws2"), append([]string{"--dry-run"}, EngineArgs(c)...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("nfqws2 --dry-run: %w: %s", err, tail(out, 2000))
	}
	return nil
}

func tail(b []byte, n int) []byte {
	if len(b) > n {
		return b[len(b)-n:]
	}
	return b
}
func (r *Router) restoreSysctl() error {
	var first error
	for path, value := range map[string][]byte{"/proc/sys/net/netfilter/nf_conntrack_tcp_be_liberal": r.liberal, "/proc/sys/net/netfilter/nf_conntrack_acct": r.acct} {
		if len(value) == 0 {
			continue
		}
		if err := writeExistingSysctl(path, value); err != nil && !os.IsNotExist(err) {
			r.log.Add("Не удалось восстановить sysctl: " + err.Error())
			first = errors.Join(first, err)
		}
	}
	if first == nil {
		r.liberal = nil
		r.acct = nil
	}
	return first
}
func (r *Router) Stop() error { r.mu.Lock(); defer r.mu.Unlock(); return r.stop() }
func (r *Router) stop() error {
	if r.rules == nil {
		b, err := os.ReadFile(r.journal)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if err == nil {
			var journal firewallJournal
			if err = json.Unmarshal(b, &journal); err != nil {
				return fmt.Errorf("повреждён журнал firewall: %w", err)
			}
			for _, rule := range journal.Rules {
				if (rule.Tool != "iptables" && rule.Tool != "ip6tables") || !strings.Contains(strings.Join(rule.Args, " "), "--comment keenetic-pilot") {
					return errors.New("неизвестное правило в журнале; очистка прервана")
				}
			}
			r.rules = journal.Rules
			r.liberal = journal.Liberal
			r.acct = journal.Acct
		}
	}
	err := RemoveRules(RunCommand, r.rules)
	if r.cmd != nil {
		_ = r.cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-r.done:
		case <-time.After(3 * time.Second):
			_ = r.cmd.Process.Kill()
			<-r.done
		}
		r.cmd = nil
	}
	err = errors.Join(err, r.restoreSysctl())
	if err == nil && r.rules != nil {
		r.rules = nil
		_ = os.Remove(r.journal)
	}
	return err
}

// Development mode deliberately cannot pretend to run a packet engine.
type Development struct{}

func (Development) Kind() string  { return "development" }
func (Development) Running() bool { return false }
func (Development) Start(Config) error {
	return errors.New("режим разработки: NFQUEUE доступен только в --mode router")
}
func (Development) Stop() error   { return nil }
func (Development) Repair() error { return nil }
