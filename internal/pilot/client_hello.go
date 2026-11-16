package pilot

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type helloSample struct {
	Scope DomainScope
	Peer  string
	At    int64
	Wire  []byte
}

var errHelloUnmeasured = errors.New("неполное измерение формы ClientHello")

func clientHelloName(wire []byte) (string, string, error) {
	bad := func() (string, string, error) { return "", "", errHelloUnmeasured }
	if len(wire) < 45 || len(wire) > 4096 || wire[0] != 22 || int(binary.BigEndian.Uint16(wire[3:5]))+5 != len(wire) {
		return bad()
	}
	b := wire[5:]
	if b[0] != 1 || int(b[1])<<16|int(b[2])<<8|int(b[3]) != len(b)-4 || b[4] != 3 || b[5] != 3 {
		return bad()
	}
	pos := 38
	session := int(b[pos])
	pos++
	if pos+session+2 > len(b) {
		return bad()
	}
	pos += session
	ciphers := int(binary.BigEndian.Uint16(b[pos : pos+2]))
	pos += 2
	if ciphers == 0 || ciphers%2 != 0 || pos+ciphers+1 > len(b) {
		return bad()
	}
	pos += ciphers
	compression := int(b[pos])
	pos++
	if pos+compression+2 > len(b) {
		return bad()
	}
	pos += compression
	ext := int(binary.BigEndian.Uint16(b[pos : pos+2]))
	pos += 2
	if pos+ext != len(b) {
		return bad()
	}
	host := ""
	version := "tls12"
	for pos < len(b) {
		if pos+4 > len(b) {
			return bad()
		}
		kind := binary.BigEndian.Uint16(b[pos : pos+2])
		n := int(binary.BigEndian.Uint16(b[pos+2 : pos+4]))
		pos += 4
		if pos+n > len(b) {
			return bad()
		}
		value := b[pos : pos+n]
		pos += n
		if kind == 43 {
			if len(value) < 1 || int(value[0])+1 != len(value) || value[0]%2 != 0 {
				return bad()
			}
			for j := 1; j+1 < len(value); j += 2 {
				if binary.BigEndian.Uint16(value[j:j+2]) == 772 {
					version = "tls13"
				}
			}
		}
		if kind == 0 {
			if len(value) < 5 || int(binary.BigEndian.Uint16(value[:2]))+2 != len(value) {
				return bad()
			}
			for j := 2; j < len(value); {
				if j+3 > len(value) {
					return bad()
				}
				typ := value[j]
				length := int(binary.BigEndian.Uint16(value[j+1 : j+3]))
				j += 3
				if j+length > len(value) {
					return bad()
				}
				if typ == 0 {
					if host != "" {
						return bad()
					}
					host = strings.ToLower(string(value[j : j+length]))
				}
				j += length
			}
		}
	}
	if !ValidDomain(host) {
		return bad()
	}
	return host, version, nil
}

func readHelloSample(c Config, s DomainScope) (helloSample, error) {
	var latest helloSample
	now := time.Now().Unix()
	for i := 0; i < 64; i++ {
		path := filepath.Join(c.RuntimeDir, fmt.Sprintf("hello%02d.tsv", i))
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		data, err := io.ReadAll(io.LimitReader(f, 9001))
		f.Close()
		if err != nil || len(data) > 9000 {
			continue
		}
		lines := strings.Split(string(data), "\n")
		if len(lines) != 3 || lines[1] != "END" || lines[2] != "" {
			continue
		}
		fields := strings.Split(lines[0], "\t")
		if len(fields) != 8 || fields[0] != "PILOT_HELLO1" || fields[1] != s.Host || fields[2] != s.TLS || fields[3] != s.Family || fields[4] != strconv.Itoa(s.Port) {
			continue
		}
		at, err := strconv.ParseInt(fields[6], 10, 64)
		if err != nil || at > now || now-at > 600 || at < latest.At {
			continue
		}
		peer := net.ParseIP(fields[5])
		if peer == nil || !publicIP(peer) || (s.Family == "ipv4") != (peer.To4() != nil) {
			continue
		}
		wire, err := hex.DecodeString(fields[7])
		if err != nil {
			continue
		}
		host, version, err := clientHelloName(wire)
		if err != nil || host != s.Host || version != s.TLS {
			continue
		}
		latest = helloSample{Scope: s, Peer: peer.String(), At: at, Wire: wire}
	}
	if latest.Wire == nil {
		return latest, fmt.Errorf("нет свежего полного ClientHello ошибочного подключения; метод сохранён")
	}
	return latest, nil
}

func readServerHello(reader io.Reader, client []byte) error {
	for record := 0; record < 3; record++ {
		header := make([]byte, 5)
		if _, err := io.ReadFull(reader, header); err != nil {
			return err
		}
		n := int(binary.BigEndian.Uint16(header[3:5]))
		if header[1] != 3 || header[2] > 3 || n < 1 || n > 18432 {
			return errHelloUnmeasured
		}
		payload := make([]byte, n)
		if _, err := io.ReadFull(reader, payload); err != nil {
			return err
		}
		if header[0] == 20 && bytes.Equal(payload, []byte{1}) {
			continue
		}
		if header[0] == 21 {
			return errors.New("сервер прислал TLS alert на форму клиента")
		}
		if header[0] != 22 || len(payload) < 42 || payload[0] != 2 {
			return errHelloUnmeasured
		}
		size := int(payload[1])<<16 | int(payload[2])<<8 | int(payload[3])
		if size+4 > len(payload) || size < 38 {
			return errHelloUnmeasured
		}
		retry, _ := hex.DecodeString("cf21ad74e59a6111be1d8c021e65b891c2a211167abb8c5e079e09e2c8a8339c")
		if bytes.Equal(payload[6:38], retry) {
			return fmt.Errorf("%w: требуется HelloRetryRequest", errHelloUnmeasured)
		}
		hello := payload[:size+4]
		if hello[4] != 3 || hello[5] != 3 {
			return errHelloUnmeasured
		}
		sid := int(hello[38])
		if sid > 32 || 42+sid > len(hello) || hello[41+sid] != 0 {
			return errHelloUnmeasured
		}
		cipher := binary.BigEndian.Uint16(hello[39+sid : 41+sid])
		if cipher == 0 {
			return errHelloUnmeasured
		}
		_, version, err := clientHelloName(client)
		if err != nil {
			return err
		}
		b := client[5:]
		clientSID := int(b[38])
		pos := 39 + clientSID
		cipherBytes := int(binary.BigEndian.Uint16(b[pos : pos+2]))
		pos += 2
		offered := false
		for j := pos; j < pos+cipherBytes; j += 2 {
			if binary.BigEndian.Uint16(b[j:j+2]) == cipher {
				offered = true
			}
		}
		if !offered {
			return errHelloUnmeasured
		}
		if version == "tls13" && !bytes.Equal(hello[39:39+sid], b[39:39+clientSID]) {
			return errHelloUnmeasured
		}
		return nil
	}
	return errHelloUnmeasured
}

func probeClientHello(parent context.Context, c Config, sample helloSample, direct bool) error {
	timeout := time.Duration(c.TimeoutSeconds) * time.Second
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	family := "tcp4"
	if sample.Scope.Family == "ipv6" {
		family = "tcp6"
	}
	p := HTTPProber{MarkDirect: true, TrialMark: !direct, Interface: c.WAN, PinnedIP: sample.Peer}
	conn, err := p.dialPublic(ctx, sample.Scope.Host, strconv.Itoa(sample.Scope.Port), family, timeout, direct)
	if err != nil {
		return err
	}
	defer conn.Close()
	deadline, _ := ctx.Deadline()
	conn.SetDeadline(deadline)
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	if _, err = io.Copy(conn, bytes.NewReader(sample.Wire)); err != nil {
		return err
	}
	return readServerHello(conn, sample.Wire)
}
