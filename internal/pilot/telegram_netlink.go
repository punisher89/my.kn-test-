package pilot

import (
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	"github.com/mdlayher/genetlink"
	"github.com/mdlayher/netlink"
	"golang.org/x/sys/unix"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

func configureTelegramWireGuard(index int, privateKey wgtypes.Key, peer wgtypes.PeerConfig) error {
	conn, err := genetlink.Dial(nil)
	if err != nil {
		return fmt.Errorf("не удалось открыть Netlink WireGuard: %w", err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return err
	}
	// Older kernels can lack extended ACK support.
	_ = conn.SetOption(netlink.ExtendedAcknowledge, true)
	family, err := conn.GetFamily(unix.WG_GENL_NAME)
	if err != nil {
		return fmt.Errorf("ядро не предоставило интерфейс управления WireGuard: %w", err)
	}
	return applyTelegramWireGuard(uint32(index), privateKey, peer, func(data []byte) error {
		_, err := conn.Execute(genetlink.Message{
			Header: genetlink.Header{Command: unix.WG_CMD_SET_DEVICE, Version: unix.WG_GENL_VERSION},
			Data:   data,
		}, family.ID, netlink.Request|netlink.Acknowledge)
		return err
	})
}

func applyTelegramWireGuard(index uint32, privateKey wgtypes.Key, peer wgtypes.PeerConfig, send func([]byte) error) error {
	set := func(label string, encode func(*netlink.AttributeEncoder)) error {
		encoder := netlink.NewAttributeEncoder()
		encoder.Uint32(unix.WGDEVICE_A_IFINDEX, index)
		encode(encoder)
		data, err := encoder.Encode()
		if err == nil {
			err = send(data)
		}
		if err != nil {
			return fmt.Errorf("WireGuard, этап «%s»: %w", label, err)
		}
		return nil
	}

	if err := set("ключ интерфейса", func(a *netlink.AttributeEncoder) {
		a.Bytes(unix.WGDEVICE_A_PRIVATE_KEY, privateKey[:])
	}); err != nil {
		return err
	}

	setPeer := func(label string, attrs func(*netlink.AttributeEncoder) error) error {
		return set(label, func(a *netlink.AttributeEncoder) {
			a.Nested(unix.WGDEVICE_A_PEERS, func(peers *netlink.AttributeEncoder) error {
				peers.Nested(0, func(entry *netlink.AttributeEncoder) error {
					entry.Bytes(unix.WGPEER_A_PUBLIC_KEY, peer.PublicKey[:])
					if attrs != nil {
						return attrs(entry)
					}
					return nil
				})
				return nil
			})
		})
	}

	if err := setPeer("ключ сервера", nil); err != nil {
		return err
	}
	if peer.PresharedKey != nil {
		if err := setPeer("PresharedKey", func(a *netlink.AttributeEncoder) error {
			a.Bytes(unix.WGPEER_A_PRESHARED_KEY, peer.PresharedKey[:])
			return nil
		}); err != nil {
			return err
		}
	}
	if err := setPeer("Endpoint", func(a *netlink.AttributeEncoder) error {
		var address [unix.SizeofSockaddrInet4]byte
		binary.NativeEndian.PutUint16(address[:2], unix.AF_INET)
		binary.BigEndian.PutUint16(address[2:4], uint16(peer.Endpoint.Port))
		copy(address[4:8], peer.Endpoint.IP.To4())
		a.Bytes(unix.WGPEER_A_ENDPOINT, address[:])
		return nil
	}); err != nil {
		return err
	}
	if err := setPeer("PersistentKeepalive", func(a *netlink.AttributeEncoder) error {
		a.Uint16(unix.WGPEER_A_PERSISTENT_KEEPALIVE_INTERVAL, uint16(*peer.PersistentKeepaliveInterval/time.Second))
		return nil
	}); err != nil {
		return err
	}

	// Match wireguard-tools: each child of the nested AllowedIPs list uses
	// type zero. A new interface has no previous peers or prefixes to replace.
	addNetworks := func(start, end int) func(*netlink.AttributeEncoder) error {
		return func(a *netlink.AttributeEncoder) error {
			a.Nested(unix.WGPEER_A_ALLOWEDIPS, func(array *netlink.AttributeEncoder) error {
				for _, n := range peer.AllowedIPs[start:end] {
					array.Nested(0, func(entry *netlink.AttributeEncoder) error {
						bits, _ := n.Mask.Size()
						entry.Uint16(unix.WGALLOWEDIP_A_FAMILY, unix.AF_INET)
						entry.Bytes(unix.WGALLOWEDIP_A_IPADDR, n.IP.To4())
						entry.Uint8(unix.WGALLOWEDIP_A_CIDR_MASK, uint8(bits))
						return nil
					})
				}
				return nil
			})
			return nil
		}
	}
	count := len(peer.AllowedIPs)
	err := setPeer("список сетей Telegram", addNetworks(0, count))
	if !errors.Is(err, unix.ERANGE) {
		return err
	}
	// If a kernel rejects a list as a whole, send one prefix per request.
	for i, n := range peer.AllowedIPs {
		if err := setPeer("сеть Telegram "+n.String(), addNetworks(i, i+1)); err != nil {
			return err
		}
	}
	return nil
}
