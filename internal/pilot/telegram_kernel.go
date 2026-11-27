package pilot

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/vishvananda/netlink"
	"golang.zx2c4.com/wireguard/wgctrl"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

const telegramWGChain = "PILOT_WG_TG"

var telegramWGJump = []Rule{{"iptables", "nat", "POSTROUTING", []string{"-o", telegramWGName, "-j", telegramWGChain}}}

func (m *Manager) startTelegramTunnel(ctx context.Context, marker string) (result error) {
	b, err := os.ReadFile(m.telegramProfilePath())
	if err != nil {
		return errors.New("сначала сохраните профиль WireGuard")
	}
	profile, err := normalizeTelegramProfile(string(b))
	if err != nil {
		return err
	}
	settings := make(map[string]string)
	for _, line := range strings.Split(profile, "\n") {
		if key, value, ok := strings.Cut(line, " = "); ok {
			settings[key] = value
		}
	}
	var networks []net.IPNet
	for _, raw := range strings.Split(telegramAllowedIPs, ",") {
		_, network, err := net.ParseCIDR(raw)
		if err != nil {
			return err
		}
		networks = append(networks, *network)
	}
	host, port, _ := net.SplitHostPort(settings["Endpoint"])
	endpointPort, _ := strconv.Atoi(port)
	resolveCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	addresses, err := net.DefaultResolver.LookupNetIP(resolveCtx, "ip4", host)
	if err != nil || len(addresses) == 0 {
		return errors.New("не удалось найти IPv4-адрес сервера из поля Endpoint")
	}
	endpoint := net.IP(addresses[0].AsSlice())
	for _, n := range networks {
		if n.Contains(endpoint) {
			return errors.New("адрес WireGuard-сервера попадает в одну из сетей Telegram")
		}
	}

	privateKey, _ := wgtypes.ParseKey(settings["PrivateKey"])
	publicKey, _ := wgtypes.ParseKey(settings["PublicKey"])
	keepalive := 25 * time.Second
	peer := wgtypes.PeerConfig{
		PublicKey: publicKey, Endpoint: &net.UDPAddr{IP: endpoint, Port: endpointPort},
		ReplaceAllowedIPs: true, AllowedIPs: networks, PersistentKeepaliveInterval: &keepalive,
	}
	if settings["PresharedKey"] != "" {
		key, _ := wgtypes.ParseKey(settings["PresharedKey"])
		peer.PresharedKey = &key
	}

	link := &netlink.GenericLink{LinkAttrs: netlink.NewLinkAttrs(), LinkType: "wireguard"}
	link.Name = telegramWGName
	if mtu, _ := strconv.Atoi(settings["MTU"]); mtu != 0 {
		link.MTU = mtu
	} else {
		link.MTU = 1380
	}
	if err := netlink.LinkAdd(link); err != nil {
		return fmt.Errorf("не удалось создать интерфейс WireGuard ядра (%w). Проверьте компонент WireGuard VPN в KeeneticOS", err)
	}
	chainCreated := false
	defer func() {
		if result == nil {
			return
		}
		var cleanup error
		if chainCreated {
			cleanup = removeTelegramNAT()
		}
		cleanup = errors.Join(cleanup, removeTelegramLink())
		if cleanup == nil {
			_ = os.Remove(marker)
		} else {
			result = errors.Join(result, cleanup)
		}
	}()
	if err := os.WriteFile(marker, nil, 0600); err != nil {
		return errors.New("не удалось записать состояние интерфейса Telegram")
	}
	client, err := wgctrl.New()
	if err != nil {
		return fmt.Errorf("не удалось открыть WireGuard ядра: %w", err)
	}
	defer client.Close()
	if err := client.ConfigureDevice(telegramWGName, wgtypes.Config{
		PrivateKey: &privateKey, ReplacePeers: true, Peers: []wgtypes.PeerConfig{peer},
	}); err != nil {
		return fmt.Errorf("ядро отклонило конфигурацию WireGuard: %w", err)
	}
	// Reading the just-created link fills the index on every architecture.
	kernelLink, err := netlink.LinkByName(telegramWGName)
	if err != nil {
		return err
	}
	address := netip.MustParsePrefix(settings["Address"]).Addr().AsSlice()
	if err := netlink.AddrAdd(kernelLink, &netlink.Addr{IPNet: &net.IPNet{IP: net.IP(address), Mask: net.CIDRMask(32, 32)}}); err != nil {
		return fmt.Errorf("не удалось задать IPv4-адрес туннеля: %w", err)
	}
	if err := netlink.LinkSetUp(kernelLink); err != nil {
		return fmt.Errorf("не удалось поднять интерфейс Telegram: %w", err)
	}
	if err := RunCommand("iptables", "-w", "-t", "nat", "-N", telegramWGChain); err != nil {
		return fmt.Errorf("не удалось создать цепочку NAT Telegram: %w", err)
	}
	chainCreated = true
	if err := RunCommand("iptables", "-w", "-t", "nat", "-A", telegramWGChain, "-j", "MASQUERADE"); err != nil {
		return err
	}
	if err := EnsureRules(RunCommand, telegramWGJump); err != nil {
		return err
	}

	for i := range networks {
		route := netlink.Route{LinkIndex: kernelLink.Attrs().Index, Dst: &networks[i], Scope: netlink.SCOPE_LINK}
		if err := netlink.RouteAdd(&route); err != nil {
			return fmt.Errorf("не удалось добавить маршрут Telegram %s: %w", route.Dst, err)
		}
	}
	return nil
}

func removeTelegramNAT() error {
	if RunCommand("iptables", "-w", "-t", "nat", "-S", telegramWGChain) != nil {
		// Confirm the table is available before treating the chain as absent.
		return RunCommand("iptables", "-w", "-t", "nat", "-S")
	}
	if err := RemoveRules(RunCommand, telegramWGJump); err != nil {
		return err
	}
	if err := RunCommand("iptables", "-w", "-t", "nat", "-F", telegramWGChain); err != nil {
		return err
	}
	return RunCommand("iptables", "-w", "-t", "nat", "-X", telegramWGChain)
}

func removeTelegramLink() error {
	link, err := netlink.LinkByName(telegramWGName)
	if errors.As(err, new(netlink.LinkNotFoundError)) {
		return nil
	}
	if err != nil {
		return err
	}
	if link.Type() != "wireguard" {
		return errors.New("интерфейс pilotwg0 занят другим подключением")
	}
	// Deleting the link also removes IPv4 routes attached to this index.
	return netlink.LinkDel(link)
}

func stopTelegramTunnel(marker string) error {
	if err := errors.Join(removeTelegramNAT(), removeTelegramLink()); err != nil {
		return fmt.Errorf("не удалось полностью остановить туннель Telegram: %w", err)
	}
	return os.Remove(marker)
}
