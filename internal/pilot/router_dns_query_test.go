package pilot

import (
	"context"
	"errors"
	"net"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

func TestRouterDNSPlainQuery(t *testing.T) {
	for _, tc := range []struct {
		name    string
		code    dnsmessage.RCode
		wrongID bool
		want    string
	}{
		{name: "answer"},
		{name: "SERVFAIL", code: dnsmessage.RCodeServerFailure, want: "RCodeServerFailure"},
		{name: "mismatched transaction", wrongID: true, want: "не совпадает"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			listener, err := net.ListenPacket("udp4", "127.0.0.1:0")
			if err != nil {
				if errors.Is(err, syscall.EPERM) {
					t.Skip(err)
				}
				t.Fatal(err)
			}
			defer listener.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if err := listener.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
				t.Fatal(err)
			}
			client := make(chan error, 1)
			go func() { client <- queryRouterDNS(ctx, listener.LocalAddr().String()) }()
			buf := make([]byte, 512)
			n, addr, err := listener.ReadFrom(buf)
			if err != nil {
				t.Fatal(err)
			}
			var req dnsmessage.Message
			if err := req.Unpack(buf[:n]); err != nil {
				t.Fatal(err)
			}
			if req.Response || !req.RecursionDesired || len(req.Questions) != 1 || req.Questions[0].Type != dnsmessage.TypeA ||
				req.Questions[0].Name.String() != "example.com." || len(req.Additionals) != 0 {
				t.Fatalf("unexpected query: %+v", req)
			}
			res := dnsmessage.Message{Header: dnsmessage.Header{Response: true, ID: req.ID, RCode: tc.code}, Questions: req.Questions}
			if tc.wrongID {
				res.ID++
			}
			if tc.code == dnsmessage.RCodeSuccess {
				res.Answers = []dnsmessage.Resource{{
					Header: dnsmessage.ResourceHeader{Name: req.Questions[0].Name, Class: dnsmessage.ClassINET, TTL: 1},
					Body:   &dnsmessage.AResource{A: [4]byte{192, 0, 2, 1}},
				}}
			}
			response, err := res.Pack()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := listener.WriteTo(response, addr); err != nil {
				t.Fatal(err)
			}
			err = <-client
			if tc.want == "" && err != nil || tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
				t.Fatalf("want error %q, got %v", tc.want, err)
			}
		})
	}
}
