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
	Actions     []string `json:"actions,omitempty"`
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
	{ID: "fake-split", Name: "Fake split", Description: "Чередование ложных и настоящих частей ClientHello.", Actions: []string{"fakedsplit:pos=midsld:tcp_md5"}},
	{ID: "hostfake-md5", Name: "HostFake / md5", Description: "Адаптация семейства hostfakesplit (Flowseal / zapret-gui).", Actions: []string{"hostfakesplit:host=www.google.com:midhost=midsld:tcp_md5"}},
	{ID: "fake-host-md5", Name: "Fake + HostFake / md5", Description: "Ложный ClientHello и разделение с подставным именем.", Actions: []string{"fake:blob=fake_default_tls:tcp_md5:repeats=6:tls_mod=rnd,dupsid,sni=www.google.com", "hostfakesplit:host=www.google.com:midhost=midsld:tcp_md5"}},
	{ID: "fake-split-md5", Name: "Fake + split / md5", Description: "Семейство fake,multisplit с TLS random и SNI Google.", Actions: []string{"fake:blob=fake_default_tls:tcp_md5:repeats=8:tls_mod=rnd,dupsid,sni=www.google.com", "multisplit:pos=1,midsld"}},
	{ID: "hostfake-ts", Name: "HostFake / ts", Description: "Адаптация семейства hostfakesplit (Flowseal / zapret-gui).", Actions: []string{"hostfakesplit:host=www.google.com:midhost=midsld:tcp_ts=-600000:tcp_ts_up"}},
	{ID: "fake-host-ts", Name: "Fake + HostFake / ts", Description: "Ложный ClientHello и разделение с подставным именем.", Actions: []string{"fake:blob=fake_default_tls:tcp_ts=-600000:tcp_ts_up:repeats=6:tls_mod=rnd,dupsid,sni=www.google.com", "hostfakesplit:host=www.google.com:midhost=midsld:tcp_ts=-600000:tcp_ts_up"}},
	{ID: "fake-split-ts", Name: "Fake + split / ts", Description: "Семейство fake,multisplit с TLS random и SNI Google.", Actions: []string{"fake:blob=fake_default_tls:tcp_ts=-600000:tcp_ts_up:repeats=8:tls_mod=rnd,dupsid,sni=www.google.com", "multisplit:pos=1,midsld"}},
	{ID: "hostfake-seq", Name: "HostFake / seq", Description: "Адаптация семейства hostfakesplit (Flowseal / zapret-gui).", Actions: []string{"hostfakesplit:host=www.google.com:midhost=midsld:tcp_seq=-10000"}},
	{ID: "fake-host-seq", Name: "Fake + HostFake / seq", Description: "Ложный ClientHello и разделение с подставным именем.", Actions: []string{"fake:blob=fake_default_tls:tcp_seq=-10000:repeats=6:tls_mod=rnd,dupsid,sni=www.google.com", "hostfakesplit:host=www.google.com:midhost=midsld:tcp_seq=-10000"}},
	{ID: "fake-split-seq", Name: "Fake + split / seq", Description: "Семейство fake,multisplit с TLS random и SNI Google.", Actions: []string{"fake:blob=fake_default_tls:tcp_seq=-10000:repeats=8:tls_mod=rnd,dupsid,sni=www.google.com", "multisplit:pos=1,midsld"}},
	{ID: "overlap-1", Name: "Split / SEQ overlap 1", Description: "Адаптация SEQ-overlap с встроенным TLS-шаблоном.", Actions: []string{"multisplit:pos=1,midsld:seqovl=1:seqovl_pattern=fake_default_tls"}},
	{ID: "disorder-overlap-1", Name: "Disorder / SEQ overlap 1", Description: "Перестановка сегментов с SEQ-overlap.", Actions: []string{"multidisorder:pos=1,midsld:seqovl=1:seqovl_pattern=fake_default_tls"}},
	{ID: "overlap-32", Name: "Split / SEQ overlap 32", Description: "Адаптация SEQ-overlap с встроенным TLS-шаблоном.", Actions: []string{"multisplit:pos=1,midsld:seqovl=32:seqovl_pattern=fake_default_tls"}},
	{ID: "disorder-overlap-32", Name: "Disorder / SEQ overlap 32", Description: "Перестановка сегментов с SEQ-overlap.", Actions: []string{"multidisorder:pos=1,midsld:seqovl=32:seqovl_pattern=fake_default_tls"}},
	{ID: "overlap-256", Name: "Split / SEQ overlap 256", Description: "Адаптация SEQ-overlap с встроенным TLS-шаблоном.", Actions: []string{"multisplit:pos=1,midsld:seqovl=256:seqovl_pattern=fake_default_tls"}},
	{ID: "fake-disorder-md5", Name: "Fake + disorder / MD5", Description: "Ложный ClientHello и обратный порядок сегментов.", Actions: []string{"fake:blob=fake_default_tls:tcp_md5:repeats=8:tls_mod=rnd,dupsid,sni=www.google.com", "multidisorder:pos=1,midsld"}},
	{ID: "overlap-681", Name: "Split / SEQ overlap 681", Description: "Адаптация SEQ-overlap с встроенным TLS-шаблоном.", Actions: []string{"multisplit:pos=1,midsld:seqovl=681:seqovl_pattern=fake_default_tls"}},
	{ID: "fake-disorder-ts", Name: "Fake + disorder / TS", Description: "Ложный ClientHello с timestamp и обратный порядок сегментов.", Actions: []string{"fake:blob=fake_default_tls:tcp_ts=-600000:tcp_ts_up:repeats=11:tls_mod=rnd,dupsid,sni=www.google.com", "multidisorder:pos=1,midsld"}},
	{ID: "fakedsplit-ts-4", Name: "Fake split / TS ×4", Description: "Семейство fakedsplit с TCP timestamp.", Actions: []string{"fakedsplit:pos=midsld:tcp_ts=-600000:tcp_ts_up:repeats=4"}},
	{ID: "fakedsplit-ts-8", Name: "Fake split / TS ×8", Description: "Семейство fakedsplit с TCP timestamp.", Actions: []string{"fakedsplit:pos=midsld:tcp_ts=-600000:tcp_ts_up:repeats=8"}},
	{ID: "fakedsplit-ts-11", Name: "Fake split / TS ×11", Description: "Семейство fakedsplit с TCP timestamp.", Actions: []string{"fakedsplit:pos=midsld:tcp_ts=-600000:tcp_ts_up:repeats=11"}},
}

func StrategyByID(id string) (Strategy, bool) {
	for _, s := range Strategies {
		if s.ID == id {
			return s, true
		}
	}
	return Strategy{}, false
}

func StrategiesFor(c Config) []Strategy {
	return append(append([]Strategy{}, Strategies...), c.Catalog...)
}
func strategyFor(c Config, id string) (Strategy, bool) {
	for _, s := range StrategiesFor(c) {
		if s.ID == id {
			return s, true
		}
	}
	return Strategy{}, false
}

func EngineArgs(c Config) []string {
	args := []string{fmt.Sprintf("--qnum=%d", c.Queue), "--fwmark=0x40000000", "--lua-init=@" + filepath.Join(c.EngineDir, "lua", "zapret-lib.lua.gz"), "--lua-init=@" + filepath.Join(c.EngineDir, "lua", "zapret-antidpi.lua.gz")}
	if c.Adaptive && !c.tuning {
		args = append(args, "--lua-init=@"+filepath.Join(c.EngineDir, "lua", "zapret-auto.lua.gz"), "--lua-init=pilot_runtime="+luaString(c.RuntimeDir), "--lua-init=@"+filepath.Join(filepath.Dir(c.EngineDir), "lua", "pilot-auto.lua"))
	}
	haveProfile := false
	profile := func(a ...string) {
		if haveProfile {
			args = append(args, "--new")
		}
		args = append(args, a...)
		haveProfile = true
	}
	exclude := []string{}
	if len(c.Exclusions) > 0 {
		exclude = append(exclude, "--hostlist-exclude-domains="+strings.Join(c.Exclusions, ","))
	}
	for _, p := range c.Profiles {
		s, _ := strategyFor(c, p.Strategy)
		if len(s.Actions) > 0 || (c.Adaptive && !c.tuning) {
			profile("--filter-tcp=443,2053,2083,2087,2096,8443", "--filter-l7=tls", "--hostlist-domains="+strings.Join(p.Domains, ","))
			args = append(args, exclude...)
			if c.Adaptive && !c.tuning {
				args = appendAdaptive(args, c, p.ID+"_"+p.Strategy, p.Strategy)
			} else {
				args = append(args, "--payload=tls_client_hello")
				for _, a := range s.Actions {
					args = append(args, "--lua-desync="+a)
				}
			}
			if len(s.Actions) > 0 {
				profile("--filter-tcp=80", "--filter-l7=http", "--hostlist-domains="+strings.Join(p.Domains, ","))
				args = append(args, exclude...)
				args = append(args, "--payload=http_req", "--lua-desync=multisplit:pos=method+2")
			}
		}
		if c.QUIC {
			profile("--filter-udp=443", "--filter-l7=quic", "--hostlist-domains="+strings.Join(p.Domains, ","))
			args = append(args, exclude...)
			args = append(args, "--payload=quic_initial", "--lua-desync=fake:blob=fake_default_quic:repeats=6")
		}
	}
	if c.Adaptive && !c.tuning {
		profile("--filter-tcp=443", "--filter-l7=tls")
		excluded := append([]string{}, c.Exclusions...)
		for _, p := range c.Profiles {
			excluded = append(excluded, p.Domains...)
		}
		if len(excluded) > 0 {
			args = append(args, "--hostlist-exclude-domains="+strings.Join(excluded, ","))
		}
		args = appendAdaptive(args, c, "discovered", "none")
	}
	// Discord media addresses do not expose an SNI. UDP is explicitly opt-in,
	// port scoped, payload scoped and never claimed verified by HTTPS probes.
	if c.DiscordVoice {
		profile("--filter-udp=50000-50099", "--filter-l7=discord,stun", "--payload=discord_ip_discovery,stun", "--lua-desync=fake:blob=fake_default_quic:repeats=6")
	}
	return args
}

func luaString(s string) string {
	eq := "="
	for strings.Contains(s, "]"+eq+"]") {
		eq += "="
	}
	return "[" + eq + "[" + s + "]" + eq + "]"
}
func appendAdaptive(args []string, c Config, key, preferred string) []string {
	pool := []Strategy{}
	if first, ok := strategyFor(c, preferred); ok {
		pool = append(pool, first)
	}
	for _, s := range StrategiesFor(c) {
		if s.ID != preferred {
			pool = append(pool, s)
		}
	}
	args = append(args, "--out-range=-s34228", "--in-range=-s34228", "--lua-desync=pilot_auto:key="+key+":reqhost:fails=2:time=60:retrans=1:failure_detector=pilot_failure:success_detector=pilot_success", "--payload=tls_client_hello")
	for i, s := range pool {
		actions := s.Actions
		if len(actions) == 0 {
			actions = []string{"pilot_pass"}
		}
		for _, action := range actions {
			args = append(args, fmt.Sprintf("--lua-desync=%s:strategy=%d:pilot_id=%s", action, i+1, s.ID))
		}
	}
	return args
}
