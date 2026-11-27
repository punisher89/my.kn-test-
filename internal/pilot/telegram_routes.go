package pilot

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

type telegramRouteSample struct {
	Address string   `json:"address"`
	Status  string   `json:"status"`
	Outputs []string `json:"outputs,omitempty"`
}

type telegramNetworkPaths struct {
	Network string                `json:"network"`
	Samples []telegramRouteSample `json:"samples"`
}

type telegramRouteComparison struct {
	Router  []telegramNetworkPaths `json:"router"`
	Client  []telegramNetworkPaths `json:"client"`
	Source  string                 `json:"source"`
	Ingress string                 `json:"ingress"`
}

type telegramLocalNetwork struct {
	Index   int
	Address netip.Prefix
}

func (m *Manager) checkTelegramRoutes(ctx context.Context, input string) (telegramRouteComparison, error) {
	var result telegramRouteComparison
	if m.backend.Kind() != "router" {
		return result, errors.New("проверка доступна в режиме роутера")
	}
	marker := filepath.Join(filepath.Dir(m.path), telegramWGName+".active")
	if _, err := os.Stat(marker); err != nil {
		return result, errors.New("сначала запустите туннель Telegram")
	}
	iface, err := net.InterfaceByName(telegramWGName)
	if err != nil || iface.Flags&net.FlagUp == 0 {
		return result, errors.New("интерфейс туннеля отсутствует или выключен")
	}
	interfaces, err := net.Interfaces()
	if err != nil {
		return result, errors.New("не удалось прочитать список интерфейсов роутера")
	}
	names := make(map[int]string, len(interfaces))
	for _, item := range interfaces {
		names[item.Index] = item.Name
	}
	var options *netlink.RouteGetOptions
	if input = strings.TrimSpace(input); input != "" {
		client, err := netip.ParseAddr(input)
		if err != nil || !client.Is4() || !client.IsPrivate() {
			return result, errors.New("введите локальный IPv4 устройства, например 192.168.1.42")
		}
		var local []telegramLocalNetwork
		for _, item := range interfaces {
			if item.Index == iface.Index || item.Flags&net.FlagUp == 0 || item.Flags&(net.FlagLoopback|net.FlagPointToPoint) != 0 {
				continue
			}
			addresses, err := item.Addrs()
			if err != nil {
				continue
			}
			for _, address := range addresses {
				prefix, err := netip.ParsePrefix(address.String())
				if err == nil && prefix.Addr().Is4() {
					local = append(local, telegramLocalNetwork{Index: item.Index, Address: prefix})
				}
			}
		}
		index, err := telegramClientIngress(client, local)
		if err != nil {
			return result, err
		}
		result.Source, result.Ingress = client.String(), names[index]
		options = &netlink.RouteGetOptions{SrcAddr: net.IP(client.AsSlice()), IifIndex: index}
	}
	handle, err := netlink.NewHandle(unix.NETLINK_ROUTE)
	if err != nil {
		return result, fmt.Errorf("не удалось открыть таблицу маршрутизации: %w", err)
	}
	defer handle.Close()
	if err := handle.SetSocketTimeout(350 * time.Millisecond); err != nil {
		return result, fmt.Errorf("не удалось ограничить время проверки маршрутов: %w", err)
	}
	result.Router, err = telegramRoutePaths(ctx, iface.Index, names, handle.RouteGet)
	if err != nil {
		return result, err
	}
	if options != nil {
		result.Client, err = telegramRoutePaths(ctx, iface.Index, names, func(dst net.IP) ([]netlink.Route, error) {
			return handle.RouteGetWithOptions(dst, options)
		})
		if err != nil {
			return result, err
		}
	}
	current, err := net.InterfaceByName(telegramWGName)
	if err != nil || current.Index != iface.Index || current.Flags&net.FlagUp == 0 {
		return result, errors.New("туннель изменился во время проверки. Повторите проверку")
	}
	return result, nil
}

func telegramClientIngress(client netip.Addr, local []telegramLocalNetwork) (int, error) {
	index, bits := 0, -1
	ambiguous, reserved := false, false
	for _, item := range local {
		prefix := item.Address
		if client == prefix.Addr() {
			return 0, errors.New("введите адрес устройства: этот IPv4 принадлежит роутеру")
		}
		if !prefix.Contains(client) || prefix.Bits() < bits {
			continue
		}
		boundary := prefix.Bits() < 31 &&
			(client == prefix.Masked().Addr() || client == netip.AddrFrom4(telegramLastIPv4(prefix)))
		if prefix.Bits() > bits {
			index, bits, ambiguous, reserved = item.Index, prefix.Bits(), false, boundary
		} else {
			ambiguous = ambiguous || item.Index != index
			reserved = reserved || boundary
		}
	}
	if ambiguous {
		return 0, errors.New("этот адрес подходит нескольким входным интерфейсам")
	}
	if reserved {
		return 0, errors.New("введите IPv4 устройства, а не адрес сети или рассылки")
	}
	if index == 0 {
		return 0, errors.New("адрес отсутствует в напрямую подключённых сетях роутера")
	}
	return index, nil
}

func telegramLastIPv4(prefix netip.Prefix) [4]byte {
	end := prefix.Masked().Addr().As4()
	for i, mask := range net.CIDRMask(prefix.Bits(), 32) {
		end[i] |= ^mask
	}
	return end
}

func telegramRoutePaths(ctx context.Context, tunnelIndex int, names map[int]string, lookup func(net.IP) ([]netlink.Route, error)) ([]telegramNetworkPaths, error) {
	var report []telegramNetworkPaths
	for _, raw := range strings.Split(telegramAllowedIPs, ",") {
		network := netip.MustParsePrefix(raw)
		start := network.Addr().As4()
		end := telegramLastIPv4(network)
		start[3]++
		end[3]--
		item := telegramNetworkPaths{Network: raw}
		for _, address := range []netip.Addr{netip.AddrFrom4(start), netip.AddrFrom4(end)} {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			sample := telegramRouteSample{Address: address.String(), Status: "unavailable"}
			routes, err := lookup(net.IP(address.AsSlice()))
			if err == nil && len(routes) > 0 {
				sample.Status = "tunnel"
				seen := make(map[int]bool)
				for _, route := range routes {
					if route.Type != unix.RTN_UNICAST || route.LinkIndex <= 0 {
						sample.Status = "unavailable"
						continue
					}
					if route.LinkIndex != tunnelIndex && sample.Status != "unavailable" {
						sample.Status = "other"
					}
					if !seen[route.LinkIndex] {
						seen[route.LinkIndex] = true
						output := names[route.LinkIndex]
						if output == "" {
							output = fmt.Sprintf("ifindex %d", route.LinkIndex)
						}
						sample.Outputs = append(sample.Outputs, output)
					}
				}
			}
			item.Samples = append(item.Samples, sample)
		}
		report = append(report, item)
	}
	return report, nil
}
