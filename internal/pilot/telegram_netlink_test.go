package pilot

import (
	"encoding/binary"
	"errors"
	"net"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mdlayher/netlink"
	"golang.org/x/sys/unix"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

func TestTelegramWireGuardUsesZeroTypeAndRetriesSinglePrefixes(t *testing.T) {
	var privateKey, publicKey wgtypes.Key
	privateKey[0], publicKey[0] = 1, 2
	keepalive := 25 * time.Second
	peer := wgtypes.PeerConfig{PublicKey: publicKey, Endpoint: &net.UDPAddr{IP: net.IPv4(192, 0, 2, 3), Port: 51820}, PersistentKeepaliveInterval: &keepalive}
	for _, raw := range strings.Split(telegramAllowedIPs, ",") {
		_, n, err := net.ParseCIDR(raw)
		if err != nil {
			t.Fatal(err)
		}
		peer.AllowedIPs = append(peer.AllowedIPs, *n)
	}
	var peerFields []uint16
	var singles []string
	attemptedBatch := false
	err := applyTelegramWireGuard(43, privateKey, peer, func(data []byte) error {
		a := testTelegramAttrs(t, data)
		if len(a) != 2 || a[0].Type != unix.WGDEVICE_A_IFINDEX || binary.NativeEndian.Uint32(a[0].Data) != 43 {
			t.Fatal("wrong target interface")
		}
		if a[1].Type == unix.WGDEVICE_A_PRIVATE_KEY {
			if !slices.Equal(a[1].Data, privateKey[:]) {
				t.Fatal("wrong interface key bytes")
			}
			return nil
		}
		array := testTelegramAttrs(t, a[1].Data)
		if len(array) != 1 || array[0].Type != netlink.Nested {
			t.Fatalf("invalid peer array entry type")
		}
		fields := testTelegramAttrs(t, array[0].Data)
		if fields[0].Type != unix.WGPEER_A_PUBLIC_KEY || !slices.Equal(fields[0].Data, publicKey[:]) {
			t.Fatal("wrong peer key bytes")
		}
		if len(fields) == 1 {
			return nil
		}
		attr := fields[1]
		fieldType := attr.Type &^ netlink.Nested
		peerFields = append(peerFields, fieldType)
		if fieldType == unix.WGPEER_A_ENDPOINT {
			if len(attr.Data) != unix.SizeofSockaddrInet4 ||
				binary.NativeEndian.Uint16(attr.Data[:2]) != unix.AF_INET ||
				binary.BigEndian.Uint16(attr.Data[2:4]) != 51820 ||
				!net.IP(attr.Data[4:8]).Equal(peer.Endpoint.IP) {
				t.Fatal("wrong IPv4 endpoint layout")
			}
		}
		if fieldType != unix.WGPEER_A_ALLOWEDIPS {
			return nil
		}
		prefixes := testTelegramAttrs(t, attr.Data)
		for _, prefix := range prefixes {
			if prefix.Type != netlink.Nested {
				t.Fatalf("non-zero WireGuard array index: %#x", prefix.Type)
			}
		}
		if len(prefixes) > 1 {
			attemptedBatch = true
			return &netlink.OpError{Err: unix.ERANGE}
		}
		f := testTelegramAttrs(t, prefixes[0].Data)
		singles = append(singles, (&net.IPNet{IP: f[1].Data, Mask: net.CIDRMask(int(f[2].Data[0]), 32)}).String())
		return nil
	})
	if err != nil || !attemptedBatch || strings.Join(singles, ",") != telegramAllowedIPs {
		t.Fatalf("allowed-IP fallback failed: %v, %v", singles, err)
	}
	if len(peerFields) < 3 || !slices.Equal(peerFields[:3], []uint16{unix.WGPEER_A_ENDPOINT, unix.WGPEER_A_PERSISTENT_KEEPALIVE_INTERVAL, unix.WGPEER_A_ALLOWEDIPS}) {
		t.Fatalf("inaccurate configuration stages: %v", peerFields)
	}
	err = applyTelegramWireGuard(43, privateKey, peer, func([]byte) error { return unix.ERANGE })
	if !errors.Is(err, unix.ERANGE) || !strings.Contains(err.Error(), "ключ интерфейса") {
		t.Fatalf("missing failure stage: %v", err)
	}
}

func testTelegramAttrs(t *testing.T, data []byte) []netlink.Attribute {
	t.Helper()
	attrs, err := netlink.UnmarshalAttributes(data)
	if err != nil {
		t.Fatal(err)
	}
	return attrs
}
