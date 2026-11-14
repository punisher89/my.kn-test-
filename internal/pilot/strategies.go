package pilot

import (
	"fmt"
	"path/filepath"
	"strings"
)

type Strategy struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Actions     []string `json:"-"`
}

// Fixed argument vectors, never shell fragments. All actions are from nfqws2's
// zapret-antidpi library. No alteration of the actual negotiated TLS version.
var Strategies = []Strategy{
	{ID: "none", Name: "Без вмешательства", Description: "Обычное соединение; выбирается, если все прямые проверки проходят."},
	{ID: "split-sni", Name: "Разделение SNI", Description: "Разделение ClientHello внутри имени сервера.", Actions: []string{"multisplit:pos=1,midsld"}},
	{ID: "split-host", Name: "Разделение имени", Description: "Разделение перед и после SNI.", Actions: []string{"multisplit:pos=1,host,endhost"}},
	{ID: "disorder", Name: "Изменение порядка", Description: "Отправка частей ClientHello в обратном порядке.", Actions: []string{"multidisorder:pos=1,midsld"}},
	{ID: "fake-md5", Name: "Fake + MD5", Description: "Ложный ClientHello с TCP MD5 и разделение SNI.", Actions: []string{"fake:blob=fake_default_tls:tcp_md5:repeats=6", "multisplit:pos=1,midsld"}},
	{ID: "fake-seq", Name: "Fake + SEQ", Description: "Ложный ClientHello с неверным номером последовательности.", Actions: []string{"fake:blob=fake_default_tls:tcp_seq=-10000:repeats=6", "multidisorder:pos=1,midsld"}},
	{ID: "fake-autottl", Name: "Fake + auto TTL", Description: "TTL ложного пакета определяется по ответу сервера.", Actions: []string{"fake:blob=fake_default_tls:ip_autottl=-1,3-20:ip6_autottl=-1,3-20:repeats=6", "multisplit:pos=1,midsld"}},
	{ID: "fake-split", Name: "Fake split", Description: "Чередование ложных и настоящих частей ClientHello.", Actions: []string{"fakedsplit:pos=1,midsld:tcp_md5"}},
}

func StrategyByID(id string) (Strategy, bool) {
	for _, s := range Strategies {
		if s.ID == id {
			return s, true
		}
	}
	return Strategy{}, false
}

func EngineArgs(c Config) []string {
	args := []string{fmt.Sprintf("--qnum=%d", c.Queue), "--fwmark=0x40000000", "--lua-init=@" + filepath.Join(c.EngineDir, "lua", "zapret-lib.lua.gz"), "--lua-init=@" + filepath.Join(c.EngineDir, "lua", "zapret-antidpi.lua.gz")}
	profile := func(a ...string) {
		if len(args) > 5 {
			args = append(args, "--new")
		}
		args = append(args, a...)
	}
	exclude := []string{}
	if len(c.Exclusions) > 0 {
		exclude = append(exclude, "--hostlist-exclude-domains="+strings.Join(c.Exclusions, ","))
	}
	for _, p := range c.Profiles {
		s, _ := StrategyByID(p.Strategy)
		if len(s.Actions) > 0 {
			profile("--filter-tcp=443,2053,2083,2087,2096,8443", "--filter-l7=tls", "--hostlist-domains="+strings.Join(p.Domains, ","))
			args = append(args, exclude...)
			args = append(args, "--payload=tls_client_hello")
			for _, a := range s.Actions {
				args = append(args, "--lua-desync="+a)
			}
			profile("--filter-tcp=80", "--filter-l7=http", "--hostlist-domains="+strings.Join(p.Domains, ","))
			args = append(args, exclude...)
			args = append(args, "--payload=http_req", "--lua-desync=multisplit:pos=method+2")
		}
		if c.QUIC {
			profile("--filter-udp=443", "--filter-l7=quic", "--hostlist-domains="+strings.Join(p.Domains, ","))
			args = append(args, exclude...)
			args = append(args, "--payload=quic_initial", "--lua-desync=fake:blob=fake_default_quic:repeats=6")
		}
	}
	// Discord media addresses do not expose an SNI. UDP is explicitly opt-in,
	// port scoped, payload scoped and never claimed verified by HTTPS probes.
	if c.DiscordVoice {
		profile("--filter-udp=50000-50099", "--filter-l7=discord,stun", "--payload=discord_ip_discovery,stun", "--lua-desync=fake:blob=fake_default_quic:repeats=6")
	}
	return args
}
