package pilot

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strings"
	"testing"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

func TestTelegramRoutePathsDistinguishesTunnelFromOtherOutputs(t *testing.T) {
	ctx := context.Background()
	report, err := telegramRoutePaths(ctx, 17, map[int]string{17: "pilotwg0", 7: "ppp0"}, func(ip net.IP) ([]netlink.Route, error) {
		switch ip.String() {
		case "91.108.56.1":
			return []netlink.Route{{LinkIndex: 17, Type: unix.RTN_UNICAST}}, nil
		case "91.108.59.254":
			return []netlink.Route{{LinkIndex: 17, Type: unix.RTN_UNICAST}, {LinkIndex: 7, Type: unix.RTN_UNICAST}}, nil
		case "91.108.4.1":
			return []netlink.Route{{Type: unix.RTN_BLACKHOLE}}, nil
		default:
			return nil, unix.ENETUNREACH
		}
	})
	if err != nil || len(report) != len(strings.Split(telegramAllowedIPs, ",")) {
		t.Fatalf("wrong route report: %d, %v", len(report), err)
	}
	for _, item := range report {
		net := netip.MustParsePrefix(item.Network)
		if len(item.Samples) != 2 {
			t.Fatalf("wrong sample count in %s", net)
		}
		for _, sample := range item.Samples {
			address := netip.MustParseAddr(sample.Address)
			if !net.Contains(address) || address == net.Addr() {
				t.Fatalf("sample %s is outside usable range %s", address, net)
			}
		}
	}
	first := report[0].Samples
	if first[0].Address != "91.108.56.1" || first[0].Status != "tunnel" ||
		first[1].Address != "91.108.59.254" || first[1].Status != "other" || strings.Join(first[1].Outputs, ",") != "pilotwg0,ppp0" {
		t.Fatalf("wrong output selection: %+v", first)
	}
	if report[1].Samples[0].Status != "unavailable" || report[1].Samples[1].Status != "unavailable" {
		t.Fatal("blackhole or missing lookup shown as reachable")
	}
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	_, err = telegramRoutePaths(ctx, 17, nil, func(net.IP) ([]netlink.Route, error) {
		t.Fatal("lookup performed after cancellation")
		return nil, nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("lost cancellation: %v", err)
	}
}
