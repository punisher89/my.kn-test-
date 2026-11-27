package pilot

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
)

const telegramAllowedIPs = "91.108.56.0/22,91.108.4.0/22,91.108.8.0/22,91.108.16.0/22,91.108.12.0/22,149.154.160.0/20,91.105.192.0/23,91.108.20.0/22,185.76.151.0/24"

func normalizeTelegramProfile(input string) (string, error) {
	input = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(input), "\uFEFF"))
	if input == "" || len(input) > 8192 {
		return "", errors.New("вставьте текст клиентского профиля WireGuard объёмом до 8 КиБ")
	}
	if strings.HasPrefix(strings.ToLower(input), "vless://") {
		return "", errors.New("это ссылка VLESS. Для туннеля Telegram нужен клиентский профиль WireGuard (.conf)")
	}

	client, peer := make(map[string]string), make(map[string]string)
	var section map[string]string
	hasInterface, hasPeer := false, false
	for i, raw := range strings.Split(input, "\n") {
		line, _, _ := strings.Cut(raw, "#")
		line = strings.TrimSpace(line)
		switch strings.ToLower(line) {
		case "":
			continue
		case "[interface]":
			if !hasInterface {
				hasInterface, section = true, client
				continue
			}
		case "[peer]":
			if hasInterface && !hasPeer {
				hasPeer, section = true, peer
				continue
			}
			return "", errors.New("импорт поддерживает один раздел [Peer]")
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok || section == nil {
			return "", fmt.Errorf("строка %d: ожидается [Interface], [Peer] или параметр WireGuard", i+1)
		}
		key := strings.ToLower(strings.TrimSpace(k))
		allowed := " privatekey address mtu dns table listenport "
		if hasPeer {
			allowed = " publickey presharedkey endpoint allowedips persistentkeepalive "
		}
		if key == "" || strings.ContainsAny(key, " \t") || !strings.Contains(allowed, " "+key+" ") {
			return "", fmt.Errorf("строка %d: параметр не поддерживается. Нужен обычный WireGuard-профиль", i+1)
		}
		if _, exists := section[key]; exists {
			return "", fmt.Errorf("строка %d: параметр указан повторно", i+1)
		}
		section[key] = strings.TrimSpace(v)
	}
	for _, field := range []struct{ name, value string }{
		{"PrivateKey", client["privatekey"]}, {"PublicKey", peer["publickey"]}, {"PresharedKey", peer["presharedkey"]},
	} {
		if field.name == "PresharedKey" && field.value == "" {
			continue
		}
		key, err := base64.StdEncoding.DecodeString(field.value)
		if err != nil || len(key) != 32 || len(field.value) != 44 || strings.ContainsAny(field.value, "\r\n") {
			return "", fmt.Errorf("проверьте поле %s в профиле WireGuard", field.name)
		}
	}
	var address netip.Addr
	for _, entry := range strings.Split(client["address"], ",") {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(entry))
		if err != nil || prefix.Bits() < 1 {
			return "", errors.New("в поле Address нужен адрес WireGuard с маской, например 10.77.66.2/24")
		}
		if prefix.Addr().Is6() {
			continue
		}
		if address.IsValid() || !prefix.Addr().IsPrivate() {
			return "", errors.New("в поле Address нужен один частный IPv4-адрес WireGuard")
		}
		address = prefix.Addr()
	}
	if !address.IsValid() {
		return "", errors.New("в поле Address отсутствует частный IPv4-адрес WireGuard")
	}
	host, port, err := net.SplitHostPort(peer["endpoint"])
	n, portErr := strconv.Atoi(port)
	if err != nil || !validTelegramWGHost(host) || portErr != nil || n < 1 || n > 65535 {
		return "", errors.New("в поле Endpoint нужен IPv4-адрес или имя WireGuard-сервера с UDP-портом")
	}
	out := fmt.Sprintf("[Interface]\nPrivateKey = %s\nAddress = %s/32\n", client["privatekey"], address)
	if mtu := client["mtu"]; mtu != "" {
		n, err := strconv.Atoi(mtu)
		if err != nil || n < 1280 || n > 1500 {
			return "", errors.New("MTU должен быть от 1280 до 1500")
		}
		out += fmt.Sprintf("MTU = %d\n", n)
	}
	out += "\n[Peer]\nPublicKey = " + peer["publickey"] + "\n"
	if key := peer["presharedkey"]; key != "" {
		out += "PresharedKey = " + key + "\n"
	}
	return out + fmt.Sprintf("Endpoint = %s:%d\nAllowedIPs = %s\nPersistentKeepalive = 25\n", host, n, telegramAllowedIPs), nil
}

func validTelegramWGHost(host string) bool {
	if host == "" || len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(strings.TrimSuffix(host, "."), ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}
