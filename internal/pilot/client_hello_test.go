package pilot

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func actualHello(t *testing.T, version uint16) []byte {
	t.Helper()
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = tls.Client(client, &tls.Config{ServerName: "www.youtube.com", MinVersion: version, MaxVersion: version}).HandshakeContext(ctx)
	}()
	header := make([]byte, 5)
	server.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := io.ReadFull(server, header); err != nil {
		t.Fatal(err)
	}
	n := int(binary.BigEndian.Uint16(header[3:5]))
	body := make([]byte, n)
	if _, err := io.ReadFull(server, body); err != nil {
		t.Fatal(err)
	}
	client.Close()
	server.Close()
	<-done
	return append(header, body...)
}
func TestClientHelloSamplesAreBoundToVisibleNameVersionAndPublicPeer(t *testing.T) {
	_, c, _, scope := activeFixture(t)
	for _, version := range []uint16{tls.VersionTLS12, tls.VersionTLS13} {
		wire := actualHello(t, version)
		host, kind, err := clientHelloName(wire)
		if err != nil || host != "www.youtube.com" {
			t.Fatal("actual Go ClientHello not parsed", host, kind, err)
		}
		expected := "tls12"
		if version == tls.VersionTLS13 {
			expected = "tls13"
		}
		if kind != expected {
			t.Fatal("TLS versions conflated")
		}
		for n := 0; n < len(wire); n++ {
			if _, _, err := clientHelloName(wire[:n]); err == nil {
				t.Fatal("truncated hello accepted", n)
			}
		}
		scope.TLS = kind
		write := func(host, peer string, at int64) {
			text := fmt.Sprintf("PILOT_HELLO1\t%s\t%s\tipv4\t443\t%s\t%d\t%s\nEND\n", host, kind, peer, at, hex.EncodeToString(wire))
			if err := os.WriteFile(filepath.Join(c.RuntimeDir, "hello00.tsv"), []byte(text), 0600); err != nil {
				t.Fatal(err)
			}
		}
		write(scope.Host, "8.8.8.8", time.Now().Unix())
		sample, err := readHelloSample(c, scope)
		if err != nil || !bytes.Equal(sample.Wire, wire) {
			t.Fatal("valid private sample lost", err)
		}
		for _, bad := range []struct {
			host, peer string
			at         int64
		}{{scope.Host, "127.0.0.1", time.Now().Unix()}, {scope.Host, "192.168.1.1", time.Now().Unix()}, {"other.example.com", "8.8.8.8", time.Now().Unix()}, {scope.Host, "8.8.8.8", time.Now().Unix() - 601}} {
			write(bad.host, bad.peer, bad.at)
			if _, err := readHelloSample(c, scope); err == nil {
				t.Fatal("forged/private/stale sample accepted", bad)
			}
		}
	}
}
func TestServerHelloRequiresCoherentReplyAndOfferedCipher(t *testing.T) {
	wire := actualHello(t, tls.VersionTLS12)
	client := wire[5:]
	pos := 39 + int(client[38])
	cipher := client[pos+2 : pos+4]
	body := append([]byte{2, 0, 0, 38, 3, 3}, make([]byte, 32)...)
	body = append(body, 0, cipher[0], cipher[1], 0)
	response := append([]byte{22, 3, 3, 0, byte(len(body))}, body...)
	if err := readServerHello(bytes.NewReader(response), wire); err != nil {
		t.Fatal("valid canary response rejected", err)
	}
	forged := append([]byte{}, response...)
	forged[len(forged)-3] = 0
	forged[len(forged)-2] = 0
	if err := readServerHello(bytes.NewReader(forged), wire); err == nil {
		t.Fatal("invalid cipher rewarded")
	}
	if err := readServerHello(bytes.NewReader([]byte{21, 3, 3, 0, 2, 2, 40}), wire); err == nil {
		t.Fatal("TLS alert rewarded")
	}
}
func TestLuaExportsOnlyPrivateFailedClientHelloSamples(t *testing.T) {
	c := adaptiveFixture(t)
	if err := PrepareAdaptive(c); err != nil {
		t.Fatal(err)
	}
	wire := actualHello(t, tls.VersionTLS13)
	encoded := hex.EncodeToString(wire)
	harness := strings.Replace(luaPacketHarness, "local now=1000", fmt.Sprintf("local now=%d", time.Now().Unix()-30), 1)
	script := harness + `
pilot_active=true;dofile(PILOT_SCRIPT:gsub('pilot%-auto.lua','pilot-measure.lua'))
ntop=function(raw) assert(raw=='peer');return '8.8.8.8' end
local wire=('` + encoded + `'):gsub('..',function(s)return string.char(tonumber(s,16))end)
local track={hostname='www.youtube.com',lua_state={}}
local d={track=track,outgoing=true,arg={key='discovered',reqhost='1',fails='2',time='60',retrans='3',failure_detector='pilot_failure',success_detector='pilot_success'},func_instance='pilot_auto_1',dis={tcp={th_dport=443,th_sport=45000,th_flags=0},ip={ip_dst='peer'},payload=wire},plan={{arg={strategy='1',pilot_id='split-sni'},func='pilot_pass'},{arg={strategy='2',pilot_id='fake-md5'},func='pilot_pass'}}}
pilot_auto(nil,d)
assert(track.lua_state.pilot_hello==wire,'client shape changed before capture')
local f=assert(io.open(pilot_runtime..'/hello00.tsv','r'));assert(f:read('*a')=='','healthy hello exported');f:close()
d.arg={key='discovered',reqhost='1',fails='2',time='60',retrans='3',failure_detector='pilot_failure',success_detector='pilot_success'};d.outgoing=false;d.dis.tcp.th_flags=TH_RST;d.dis.tcp.th_sport=443;d.dis.payload='';d.plan={{arg={strategy='1',pilot_id='split-sni'},func='pilot_pass'},{arg={strategy='2',pilot_id='fake-md5'},func='pilot_pass'}}
pilot_auto(nil,d)
`
	// Both Lua runtimes share fixture data; reset samples before each run inside Lua.
	script = strings.Replace(script, "ntop=function", "for i=0,63 do local f=assert(io.open(pilot_runtime..'/hello'..string.format('%02d',i)..'.tsv','w'));f:close() end\nntop=function", 1)
	runAdaptiveLua(t, c, script)
	sample, err := readHelloSample(c, DomainScope{"www.youtube.com", "tls13", "ipv4", 443})
	if err != nil || !bytes.Equal(sample.Wire, wire) {
		t.Fatal("failed original form not exported", err)
	}
	info, err := os.Stat(filepath.Join(c.RuntimeDir, "hello00.tsv"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("client sample is not private")
	}
}
