package pilot

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const telegramWGName = "pilotwg0"

type telegramTunnelState struct {
	Available  bool      `json:"available"`
	CanSave    bool      `json:"can_save"`
	Missing    []string  `json:"missing,omitempty"`
	Configured bool      `json:"configured"`
	Managed    bool      `json:"managed"`
	Running    bool      `json:"running"`
	Handshake  time.Time `json:"handshake,omitzero"`
}

func (m *Manager) telegramProfilePath() string {
	return filepath.Join(filepath.Dir(m.path), telegramWGName+".conf")
}

func (m *Manager) telegramScript() string {
	return filepath.Join(filepath.Dir(m.Config().EngineDir), "scripts", "vps-telegram-wg.sh")
}

func (m *Manager) telegramTunnelStatus(ctx context.Context) telegramTunnelState {
	state := telegramTunnelState{CanSave: m.backend.Kind() == "router"}
	if _, err := os.Stat(m.telegramScript()); err != nil {
		state.Missing = append(state.Missing, "vps-telegram-wg.sh")
	}
	for _, name := range []string{"wg", "wg-quick", "iptables", "ip", "bash"} {
		if _, err := exec.LookPath(name); err != nil {
			state.Missing = append(state.Missing, name)
		}
	}
	state.Available = state.CanSave && len(state.Missing) == 0
	if file, err := os.Lstat(m.telegramProfilePath()); err == nil {
		state.Configured = file.Mode().IsRegular() && file.Mode().Perm() == 0600
	}
	_, markErr := os.Stat(filepath.Join(filepath.Dir(m.path), telegramWGName+".active"))
	state.Managed = markErr == nil
	if !state.Managed {
		return state
	}
	if _, err := net.InterfaceByName(telegramWGName); err != nil {
		return state
	}
	state.Running = true
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if data, err := exec.CommandContext(ctx, "wg", "show", telegramWGName, "latest-handshakes").Output(); err == nil {
		fields := strings.Fields(string(data))
		if len(fields) == 2 {
			if stamp, err := strconv.ParseInt(fields[1], 10, 64); err == nil && stamp > 0 {
				state.Handshake = time.Unix(stamp, 0).UTC()
			}
		}
	}
	return state
}

func (m *Manager) saveTelegramProfile(ctx context.Context, input string) error {
	profile, err := normalizeTelegramProfile(input)
	if err != nil {
		return err
	}
	dir := filepath.Dir(m.path)
	if _, err := os.Stat(filepath.Join(dir, telegramWGName+".active")); !os.IsNotExist(err) {
		return errors.New("перед заменой профиля остановите Telegram-туннель")
	}
	file, err := os.CreateTemp(dir, ".pilot-wg-*")
	if err != nil {
		return errors.New("не удалось создать временный файл профиля")
	}
	defer os.Remove(file.Name())
	_, err = file.WriteString(profile)
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		return errors.New("не удалось записать профиль")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Rename(file.Name(), m.telegramProfilePath()); err != nil {
		return errors.New("не удалось сохранить профиль в каталоге настроек Pilot")
	}
	return nil
}

func (m *Manager) changeTelegramTunnel(action string) error {
	if action != "start" && action != "stop" {
		return errors.New("неизвестное действие Telegram")
	}
	if _, err := os.Stat(m.telegramScript()); err != nil {
		return errors.New("скрипт Telegram-туннеля отсутствует в установленном пакете")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", m.telegramScript(), action)
	cmd.Env = append(os.Environ(), "PILOT_WG_CONFIG_DIR="+filepath.Dir(m.path))
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 5 * time.Second
	output, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return errors.New("истекло время операции Telegram; обновите состояние туннеля")
		}
		for _, line := range strings.Split(string(output), "\n") {
			if strings.HasPrefix(line, "Route conflict: ") || strings.HasPrefix(line, "Missing ") ||
				line == "Config must have mode 600" || line == "Interface already exists; refusing to modify it" {
				return fmt.Errorf("Telegram: %s", line)
			}
		}
		return fmt.Errorf("Telegram: операция %s завершилась ошибкой. Проверьте конфигурацию WireGuard и состояние туннеля", action)
	}
	return nil
}
