package pilot

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestRouterDNSInventory(t *testing.T) {
	raw := "system name test\n" +
		"dns-proxy\n https upstream https://actual.example/private-token?key=secret dnsm\n tls upstream 192.0.2.1 853 sni dot.example\n quic upstream 192.0.2.2 sni doq.example\n exit\n" +
		"dns-proxy https upstream https://actual.example/other\n" +
		"interface Home\n https upstream https://ignore.example/dns-query\n"
	rows := configuredDNS(raw)
	if len(rows) != 3 || rows[0].Server != "actual.example" || rows[1].Server != "dot.example" || rows[2].Server != "doq.example" || rows[0].Status != "configured" {
		t.Fatalf("unexpected rows: %#v", rows)
	}
	if len(configuredDNS("dns-proxy filter profile x https upstream https://ignore.example/dns-query")) != 0 {
		t.Fatal("profile treated as global")
	}
	a := resolvServers("nameserver 127.0.0.1\nnameserver 127.0.0.1\nnameserver 2001:db8::1\nnameserver bad\n")
	if len(a) != 2 || a[0] != "127.0.0.1" || a[1] != "2001:db8::1" {
		t.Fatalf("unexpected servers: %v", a)
	}
}
func TestDNSNoDefaultProvider(t *testing.T) {
	in, e := normalizeDNSProbe(secureDNSRequest{})
	if e != nil || in.Protocol != "router" || in.Server != "" || in.Address != "" {
		t.Fatalf("unexpected default: %#v %v", in, e)
	}
	if _, e = normalizeDNSProbe(secureDNSRequest{Protocol: "DoH"}); e == nil {
		t.Fatal("missing endpoint accepted")
	}
}

func TestRouterDNSPrefersPanelAddress(t *testing.T) {
	got := routerDNSTargets("192.168.44.1", "192.168.1.1", "192.168.44.1", "127.0.0.1", "::1", "1.1.1.1", "::")
	want := []string{"192.168.44.1", "192.168.1.1", "127.0.0.1", "::1"}
	if !slices.Equal(got, want) {
		t.Fatalf("targets = %v, want %v", got, want)
	}
}

func TestRouterDNSReadsKeeneticCurrentProxy(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	const cli = `#!/bin/sh
case "$2" in
 "show running-config") printf '%s\n' 'dns-proxy https upstream https://dns.example/private?secret=true' ;;
 "show dns-proxy") printf '%s\n' 'proxy-status:' 'dns_server = 127.0.0.1:40500 .' 'dns_server = 192.168.1.2:53 .' 'dns_server = 192.168.1.2:53 .' 'static_a = private.example 192.168.7.1' 'dns_server = invalid.example:53 .' ;;
 *) exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "ndmc"), []byte(cli), 0700); err != nil {
		t.Fatal(err)
	}
	rows := readRouterDNSConfig(context.Background())
	if len(rows) != 3 || rows[0].Server != "dns.example" || rows[1].Server != "127.0.0.1:40500" || rows[2].Server != "192.168.1.2:53" {
		t.Fatalf("unexpected inventory: %#v", rows)
	}
	for _, row := range rows {
		if row.Status != "configured" {
			t.Fatalf("inventory was marked as tested: %#v", row)
		}
	}
}
