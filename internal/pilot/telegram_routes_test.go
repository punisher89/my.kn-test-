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

func TestTelegramClientIngressChoosesTheMostSpecificConnectedInterface(t *testing.T) {
	local := []telegramLocalNetwork{
		{Index: 3, Address: netip.MustParsePrefix("192.168.1.1/16")},
		{Index: 4, Address: netip.MustParsePrefix("192.168.2.1/24")},
		{Index: 4, Address: netip.MustParsePrefix("192.168.2.2/24")},
	}
	for _, tt := range []struct {
		ip    string
		index int
	}{
		{"192.168.2.42", 4},
		{"192.168.3.42", 3},
		{"192.168.2.1", 0},
		{"192.168.2.255", 0},
		{"192.168.255.255", 0},
		{"192.168.0.0", 0},
		{"10.2.3.4", 0},
	} {
		index, err := telegramClientIngress(netip.MustParseAddr(tt.ip), local)
		if index != tt.index || (err != nil) != (tt.index == 0) {
			t.Errorf("%s: got %d (%v), want %d", tt.ip, index, err, tt.index)
		}
	}
	local = append(local, telegramLocalNetwork{Index: 5, Address: netip.MustParsePrefix("192.168.2.3/24")})
	if _, err := telegramClientIngress(netip.MustParseAddr("192.168.2.42"), local); err == nil {
		t.Fatal("overlapping same-length interface prefixes were accepted")
	}
}
