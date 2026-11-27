package pilot

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"golang.zx2c4.com/wireguard/wgctrl"
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

func (m *Manager) telegramTunnelStatus() telegramTunnelState {
	state := telegramTunnelState{CanSave: m.backend.Kind() == "router"}
	if _, err := exec.LookPath("iptables"); err != nil {
		state.Missing = []string{"iptables"}
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
	if client, err := wgctrl.New(); err == nil {
		defer client.Close()
		if device, err := client.Device(telegramWGName); err == nil && len(device.Peers) == 1 {
			state.Handshake = device.Peers[0].LastHandshakeTime.UTC()
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

func (m *Manager) changeTelegramTunnel(ctx context.Context, action, profile string) error {
	dir := filepath.Dir(m.path)
	lock := filepath.Join(dir, telegramWGName+".lock")
	if err := os.Mkdir(lock, 0700); err != nil {
		if os.IsExist(err) {
			return ErrBusy
		}
		return errors.New("не удалось заблокировать файл настроек Telegram")
	}
	defer os.Remove(lock)

	marker := filepath.Join(dir, telegramWGName+".active")
	_, err := os.Lstat(marker)
	if err != nil && !os.IsNotExist(err) {
		return errors.New("не удалось проверить состояние туннеля Telegram")
	}
	managed := err == nil
	switch action {
	case "save":
		return m.saveTelegramProfile(ctx, profile)
	case "start":
		if managed {
			return errors.New("сначала остановите текущий туннель Telegram")
		}
		return m.startTelegramTunnel(ctx, marker)
	case "stop", "delete":
		if managed {
			if err := stopTelegramTunnel(marker); err != nil {
				return err
			}
		}
		if action == "delete" {
			if err := os.Remove(m.telegramProfilePath()); err != nil && !os.IsNotExist(err) {
				return errors.New("не удалось удалить профиль Telegram")
			}
		}
		return nil
	default:
		return errors.New("неизвестное действие Telegram")
	}
}
