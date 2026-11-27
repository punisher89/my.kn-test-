package pilot

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"

	"golang.org/x/net/dns/dnsmessage"
)

// The router test sends a plain DNS A query directly to the selected endpoint.
// The OS resolver may apply hosts files, EDNS and resolv.conf even with a custom Dial.
func queryRouterDNS(ctx context.Context, endpoint string) error {
	var transaction [2]byte
	if _, err := rand.Read(transaction[:]); err != nil {
		return err
	}
	request := dnsmessage.Message{
		Header: dnsmessage.Header{ID: binary.BigEndian.Uint16(transaction[:]), RecursionDesired: true},
		Questions: []dnsmessage.Question{{
			Name: dnsmessage.MustNewName("example.com."), Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET,
		}},
	}
	packet, err := request.Pack()
	if err != nil {
		return err
	}
	response, err := exchangeRouterDNS(ctx, "udp", endpoint, packet, request)
	if err != nil {
		return err
	}
	if response.Truncated {
		response, err = exchangeRouterDNS(ctx, "tcp", endpoint, packet, request)
		if err != nil {
			return err
		}
		if response.Truncated {
			return errors.New("DNS ответил усечённым пакетом и по TCP")
		}
	}
	if response.RCode != dnsmessage.RCodeSuccess {
		return fmt.Errorf("DNS вернул %s", response.RCode)
	}
	for _, rr := range response.Answers {
		if rr.Header.Class != dnsmessage.ClassINET {
			continue
		}
		if answer, ok := rr.Body.(*dnsmessage.AResource); ok {
			ip := netip.AddrFrom4(answer.A)
			if !ip.IsUnspecified() && !ip.IsLoopback() {
				return nil
			}
		}
	}
	return errors.New("DNS ответил без пригодной A-записи")
}

func exchangeRouterDNS(ctx context.Context, network, endpoint string, request []byte, query dnsmessage.Message) (dnsmessage.Message, error) {
	var response dnsmessage.Message
	conn, err := (&net.Dialer{}).DialContext(ctx, network, endpoint)
	if err != nil {
		return response, err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			return response, err
		}
	}
	if network == "tcp" {
		request = append(binary.BigEndian.AppendUint16(nil, uint16(len(request))), request...)
	}
	if _, err := conn.Write(request); err != nil {
		return response, err
	}
	var raw []byte
	if network == "tcp" {
		var size [2]byte
		if _, err := io.ReadFull(conn, size[:]); err != nil {
			return response, err
		}
		raw = make([]byte, binary.BigEndian.Uint16(size[:]))
		if _, err := io.ReadFull(conn, raw); err != nil {
			return response, err
		}
	} else {
		var data [4096]byte
		n, err := conn.Read(data[:])
		if err != nil {
			return response, err
		}
		raw = data[:n]
	}
	var parser dnsmessage.Parser
	header, err := parser.Start(raw)
	if err != nil {
		return response, fmt.Errorf("некорректный заголовок DNS: %w", err)
	}
	question, err := parser.Question()
	if err != nil || !header.Response || header.ID != query.ID || header.OpCode != query.OpCode ||
		question != query.Questions[0] {
		return response, errors.New("DNS-ответ не совпадает с запросом")
	}
	if _, err := parser.Question(); err != dnsmessage.ErrSectionDone {
		return response, errors.New("DNS-ответ содержит несколько вопросов")
	}
	if header.Truncated {
		response.Header = header
		return response, nil
	}
	if err := response.Unpack(raw); err != nil {
		return response, fmt.Errorf("некорректный пакет DNS: %w", err)
	}
	return response, nil
}
