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

func (m *Manager) checkTelegramRoutes(ctx context.Context) ([]telegramNetworkPaths, error) {
	if m.backend.Kind() != "router" {
		return nil, errors.New("проверка доступна в режиме роутера")
	}
	marker := filepath.Join(filepath.Dir(m.path), telegramWGName+".active")
	if _, err := os.Stat(marker); err != nil {
		return nil, errors.New("сначала запустите туннель Telegram")
	}
	iface, err := net.InterfaceByName(telegramWGName)
	if err != nil || iface.Flags&net.FlagUp == 0 {
		return nil, errors.New("интерфейс туннеля отсутствует или выключен")
	}
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, errors.New("не удалось прочитать список интерфейсов роутера")
	}
	names := make(map[int]string, len(interfaces))
	for _, item := range interfaces {
		names[item.Index] = item.Name
	}
	handle, err := netlink.NewHandle(unix.NETLINK_ROUTE)
	if err != nil {
		return nil, fmt.Errorf("не удалось открыть таблицу маршрутизации: %w", err)
	}
	defer handle.Close()
	if err := handle.SetSocketTimeout(350 * time.Millisecond); err != nil {
		return nil, fmt.Errorf("не удалось ограничить время проверки маршрутов: %w", err)
	}
	report, err := telegramRoutePaths(ctx, iface.Index, names, handle.RouteGet)
	if err != nil {
		return nil, err
	}
	current, err := net.InterfaceByName(telegramWGName)
	if err != nil || current.Index != iface.Index || current.Flags&net.FlagUp == 0 {
		return nil, errors.New("туннель изменился во время проверки. Повторите проверку")
	}
	return report, nil
}

func telegramRoutePaths(ctx context.Context, tunnelIndex int, names map[int]string, lookup func(net.IP) ([]netlink.Route, error)) ([]telegramNetworkPaths, error) {
	var report []telegramNetworkPaths
	for _, raw := range strings.Split(telegramAllowedIPs, ",") {
		network := netip.MustParsePrefix(raw)
		start := network.Addr().As4()
		end := start
		for i, mask := range net.CIDRMask(network.Bits(), 32) {
			end[i] |= ^mask
		}
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
