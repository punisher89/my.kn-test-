package pilot

import (
	"embed"
	"encoding/hex"
)

// TCP/TLS actions from Flowseal 2.0.0-rc (bf168284d61aca3ed4e4b4146399b3673c43c6cb).
// Identical action chains share an ID. The preset names record their provenance.
// Pilot supplies the domain filters and applies these actions to ClientHello.
var flowsealStrategies = []Strategy{
	{ID: "flowseal-alt-discord", Name: "Flowseal ALT · Discord Media",
		Description: "ALT / Discord Media.",
		Actions: []string{
			"tls_client_hello_clone:blob=live_tls:sni_del:sni_first=www.google.com",
			"fake:blob=live_tls:optional:tcp_ts=-600000:ip_id=zero:repeats=5",
			"fake:blob=fake_default_tls:tls_mod=rnd,dupsid,sni=www.google.com:tcp_ts=-600000:ip_id=zero:repeats=3",
			"fakedsplit:pos=midsld-1:tcp_ts=-600000:ip_id=zero",
		},
	},
	{ID: "flowseal-alt-youtube", Name: "Flowseal ALT · Youtube",
		Description: "ALT / Youtube.",
		Actions: []string{
			"tls_client_hello_clone:blob=live_tls:sni_del:sni_first=www.google.com",
			"fake:blob=live_tls:optional:tcp_ts=-600000:ip_id=zero:repeats=5",
			"fake:blob=fake_default_tls:tls_mod=rnd,dupsid,sni=www.google.com:tcp_ts=-600000:ip_id=zero:repeats=3",
			"fakedsplit:pos=sld+1:tcp_ts=-600000:ip_id=zero",
		},
	},
	{ID: "flowseal-alt-domain", Name: "Flowseal ALT · Domain TLS",
		Description: "ALT / Domain TLS, ALT / IP TLS.",
		Actions: []string{
			"fake:blob=flowseal_stun2:tcp_ts=-600000:repeats=6",
			"fake:blob=flowseal_tls_google2:tcp_ts=-600000:repeats=6",
			"fakedsplit:tcp_ts=-600000:repeats=6",
		},
	},
	{ID: "flowseal-alt10-discord", Name: "Flowseal ALT10 · Discord Media",
		Description: "ALT10 / Discord Media, SIMPLE FAKE ALT2 / Discord Media, SIMPLE FAKE / Discord Media, SIMPLE FAKE / Domain TLS, SIMPLE FAKE / IP TLS.",
		Actions: []string{
			"fake:blob=flowseal_tls_google:tcp_ts=-600000:repeats=6",
		},
	},
	{ID: "flowseal-alt10-youtube", Name: "Flowseal ALT10 · Youtube",
		Description: "ALT10 / Youtube, SIMPLE FAKE ALT2 / Youtube.",
		Actions: []string{
			"fake:blob=flowseal_tls_google:tcp_ts=-600000:ip_id=zero:repeats=6",
		},
	},
	{ID: "flowseal-alt10-domain", Name: "Flowseal ALT10 · Domain TLS",
		Description: "ALT10 / Domain TLS, ALT10 / IP TLS.",
		Actions: []string{
			"fake:blob=flowseal_stun:tcp_ts=-600000:repeats=6",
			"fake:blob=flowseal_tls_4pda:tcp_ts=-600000:repeats=6",
		},
	},
	{ID: "flowseal-alt11-discord", Name: "Flowseal ALT11 · Discord Media",
		Description: "ALT11 / Discord Media, ALT12 / Discord Media, EXP / Discord Media.",
		Actions: []string{
			"fake:blob=flowseal_tls_google:tcp_ts=-600000:repeats=8",
			"multisplit:pos=1:seqovl=681:seqovl_pattern=flowseal_tls_google",
		},
	},
	{ID: "flowseal-alt11-youtube", Name: "Flowseal ALT11 · Youtube",
		Description: "ALT11 / Youtube.",
		Actions: []string{
			"fake:blob=fake_default_tls:tls_mod=rnd,dupsid,sni=www.google.com:tcp_seq=-10000:tcp_ack=-66000:ip_id=zero:repeats=11",
			"multisplit:pos=sld,sld+3,endhost-1:seqovl=600:seqovl_pattern=flowseal_tls_google3:ip_id=zero",
		},
	},
	{ID: "flowseal-alt11-domain", Name: "Flowseal ALT11 · Domain TLS",
		Description: "ALT11 / Domain TLS, ALT11 / IP TLS.",
		Actions: []string{
			"fake:blob=flowseal_stun2:tcp_ts=-600000:repeats=8",
			"fake:blob=flowseal_tls_sferum:tcp_ts=-600000:repeats=8",
			"multisplit:pos=1:seqovl=661:seqovl_pattern=flowseal_tls_sferum",
		},
	},
	{ID: "flowseal-alt12-youtube", Name: "Flowseal ALT12 · Youtube",
		Description: "ALT12 / Youtube, EXP / Youtube, SIMPLE FAKE / Youtube.",
		Actions: []string{
			"hostfakesplit:host=www.google.com:tcp_ts=-600000:ip_id=zero",
		},
	},
	{ID: "flowseal-alt12-domain", Name: "Flowseal ALT12 · Domain TLS",
		Description: "ALT12 / Domain TLS, ALT12 / IP TLS.",
		Actions: []string{
			"fake:blob=flowseal_stun:tcp_ts=-600000:repeats=8",
			"fake:blob=flowseal_tls_sferum:tcp_ts=-600000:repeats=8",
			"multisplit:pos=1:seqovl=661:seqovl_pattern=flowseal_tls_sferum",
		},
	},
	{ID: "flowseal-alt13-discord", Name: "Flowseal ALT13 · Discord Media",
		Description: "ALT13 / Discord Media.",
		Actions: []string{
			"fake:blob=flowseal_tls_google:tcp_ts=-600000:repeats=7",
			"multisplit:pos=1:seqovl=681:seqovl_pattern=flowseal_tls_google",
		},
	},
	{ID: "flowseal-alt13-youtube", Name: "Flowseal ALT13 · Youtube",
		Description: "ALT13 / Youtube.",
		Actions: []string{
			"fake:blob=fake_default_tls:tls_mod=rnd,dupsid,sni=www.google.com:tcp_ts=-600000:repeats=4",
			"hostfakesplit:host=www.google.com:tcp_ts=-600000",
			"multidisorder:pos=1,midsld",
		},
	},
	{ID: "flowseal-alt13-domain", Name: "Flowseal ALT13 · Domain TLS",
		Description: "ALT13 / Domain TLS, ALT13 / IP TLS.",
		Actions: []string{
			"fake:blob=flowseal_tls_sochi_park:tcp_ts=-600000:repeats=5",
			"fake:blob=flowseal_stun2:tcp_ts=-600000:repeats=5",
			"hostfakesplit:host=mail.ru:tcp_ts=-600000:repeats=5",
		},
	},
	{ID: "flowseal-alt14-discord", Name: "Flowseal ALT14 · Discord Media",
		Description: "ALT14 / Discord Media, ALT14 / Youtube.",
		Actions: []string{
			"fake:blob=fake_default_tls:tls_mod=rnd,dupsid,sni=www.google.com:tcp_seq=-10000:tcp_ack=-66000:ip_id=zero:repeats=7",
			"multidisorder:pos=1,midsld",
		},
	},
	{ID: "flowseal-alt14-domain", Name: "Flowseal ALT14 · Domain TLS",
		Description: "ALT14 / Domain TLS, ALT14 / IP TLS.",
		Actions: []string{
			"fake:blob=flowseal_stun2:tcp_seq=-10000:tcp_ack=-66000:repeats=3",
			"fake:blob=flowseal_tls_sochi_park:tcp_seq=-10000:tcp_ack=-66000:repeats=6",
			"multidisorder:pos=1,midsld",
		},
	},
	{ID: "flowseal-alt2-discord", Name: "Flowseal ALT2 · Discord Media",
		Description: "ALT2 / Discord Media, ALT2 / Domain TLS, ALT2 / IP TLS.",
		Actions: []string{
			"multisplit:pos=2:seqovl=652:seqovl_pattern=flowseal_tls_google",
		},
	},
	{ID: "flowseal-alt2-youtube", Name: "Flowseal ALT2 · Youtube",
		Description: "ALT2 / Youtube.",
		Actions: []string{
			"multisplit:pos=2:seqovl=652:seqovl_pattern=flowseal_tls_google:ip_id=zero",
		},
	},
	{ID: "flowseal-alt3-discord", Name: "Flowseal ALT3 · Discord Media",
		Description: "ALT3 / Discord Media.",
		Actions: []string{
			"fake:blob=fake_default_tls:tls_mod=rnd,dupsid,sni=www.google.com:tcp_ts=-600000",
			"hostfakesplit:host=www.google.com:tcp_ts=-600000",
		},
	},
	{ID: "flowseal-alt3-youtube", Name: "Flowseal ALT3 · Youtube",
		Description: "ALT3 / Youtube.",
		Actions: []string{
			"fake:blob=fake_default_tls:tls_mod=rnd,dupsid,sni=www.google.com:tcp_ts=-600000:ip_id=zero",
			"hostfakesplit:host=www.google.com:tcp_ts=-600000:ip_id=zero",
		},
	},
	{ID: "flowseal-alt3-domain", Name: "Flowseal ALT3 · Domain TLS",
		Description: "ALT3 / Domain TLS, ALT3 / IP TLS.",
		Actions: []string{
			"fake:blob=fake_default_tls:tls_mod=rnd,dupsid,sni=ya.ru:tcp_ts=-600000",
			"hostfakesplit:host=ya.ru:tcp_ts=-600000",
		},
	},
	{ID: "flowseal-alt4-discord", Name: "Flowseal ALT4 · Discord Media",
		Description: "ALT4 / Discord Media.",
		Actions: []string{
			"fake:blob=flowseal_tls_google:tcp_seq=1000:tcp_ack=-66000:tcp_ts_up:repeats=6",
			"multisplit",
		},
	},
	{ID: "flowseal-alt4-youtube", Name: "Flowseal ALT4 · Youtube",
		Description: "ALT4 / Youtube.",
		Actions: []string{
			"fake:blob=flowseal_tls_google3:tls_mod=rnd,dupsid,sni=www.google.com:tcp_seq=1000:tcp_ack=-66000:tcp_ts_up:ip_id=zero:repeats=6",
			"multisplit:pos=sld,sld+3,endhost-1:seqovl=600:seqovl_pattern=flowseal_tls_google3:ip_id=zero",
		},
	},
	{ID: "flowseal-alt4-domain", Name: "Flowseal ALT4 · Domain TLS",
		Description: "ALT4 / Domain TLS, ALT4 / IP TLS.",
		Actions: []string{
			"fake:blob=flowseal_stun:tcp_seq=1000:tcp_ack=-66000:tcp_ts_up:repeats=6",
			"fake:blob=flowseal_tls_google:tcp_seq=1000:tcp_ack=-66000:tcp_ts_up:repeats=6",
			"multisplit",
		},
	},
	{ID: "flowseal-alt5-domain", Name: "Flowseal ALT5 · Domain TLS",
		Description: "ALT5 / Domain TLS.",
		Actions: []string{
			"fake:blob=flowseal_tls_google3:tls_mod=rnd,dupsid,sni=www.google.com:tcp_seq=-10000:tcp_ack=-66000:ip_id=zero:repeats=7",
			"fake:blob=flowseal_stun2:tcp_seq=-10000:tcp_ack=-66000:ip_id=zero:repeats=3",
			"multidisorder:pos=1,midsld:ip_id=zero",
		},
	},
	{ID: "flowseal-alt6-discord", Name: "Flowseal ALT6 · Discord Media",
		Description: "ALT6 / Discord Media, ALT6 / Domain TLS, ALT6 / IP TLS, GENERAL / Discord Media.",
		Actions: []string{
			"multisplit:pos=1:seqovl=681:seqovl_pattern=flowseal_tls_google",
		},
	},
	{ID: "flowseal-alt6-youtube", Name: "Flowseal ALT6 · Youtube",
		Description: "ALT6 / Youtube, GENERAL / Youtube.",
		Actions: []string{
			"multisplit:pos=1:seqovl=681:seqovl_pattern=flowseal_tls_google:ip_id=zero",
		},
	},
	{ID: "flowseal-alt7-discord", Name: "Flowseal ALT7 · Discord Media",
		Description: "ALT7 / Discord Media, ALT7 / Domain TLS.",
		Actions: []string{
			"multisplit:pos=2,sniext+1:seqovl=679:seqovl_pattern=flowseal_tls_google",
		},
	},
	{ID: "flowseal-alt7-youtube", Name: "Flowseal ALT7 · Youtube",
		Description: "ALT7 / Youtube.",
		Actions: []string{
			"multisplit:pos=2,sniext+1:seqovl=679:seqovl_pattern=flowseal_tls_google:ip_id=zero",
		},
	},
	{ID: "flowseal-alt8-discord", Name: "Flowseal ALT8 · Discord Media",
		Description: "ALT8 / Discord Media, ALT8 / Domain TLS, ALT8 / IP TLS.",
		Actions: []string{
			"fake:blob=fake_default_tls:tcp_seq=2:tcp_ack=-66000:tcp_ts_up:repeats=6",
		},
	},
	{ID: "flowseal-alt8-youtube", Name: "Flowseal ALT8 · Youtube",
		Description: "ALT8 / Youtube.",
		Actions: []string{
			"fake:blob=fake_default_tls:tcp_seq=2:tcp_ack=-66000:tcp_ts_up:ip_id=zero:repeats=6",
		},
	},
	{ID: "flowseal-alt9-discord", Name: "Flowseal ALT9 · Discord Media",
		Description: "ALT9 / Discord Media.",
		Actions: []string{
			"hostfakesplit:host=www.google.com:tcp_ts=-600000:repeats=4",
		},
	},
	{ID: "flowseal-alt9-youtube", Name: "Flowseal ALT9 · Youtube",
		Description: "ALT9 / Youtube.",
		Actions: []string{
			"hostfakesplit:host=www.google.com:tcp_ts=-600000:ip_id=zero:repeats=4",
		},
	},
	{ID: "flowseal-alt9-domain", Name: "Flowseal ALT9 · Domain TLS",
		Description: "ALT9 / Domain TLS.",
		Actions: []string{
			"hostfakesplit:host=ozon.ru:tcp_ts=-600000:tcp_md5:repeats=4",
		},
	},
	{ID: "flowseal-alt9-ip", Name: "Flowseal ALT9 · IP TLS",
		Description: "ALT9 / IP TLS.",
		Actions: []string{
			"hostfakesplit:host=ozon.ru:tcp_ts=-600000:repeats=4",
		},
	},
	{ID: "flowseal-exp-domain", Name: "Flowseal EXP · Domain TLS",
		Description: "EXP / Domain TLS, EXP / IP TLS.",
		Actions: []string{
			"fake:blob=flowseal_tls_sferum:tcp_ts=-600000:repeats=4",
			"multisplit:pos=1:seqovl=480:seqovl_pattern=flowseal_stun2",
		},
	},
	{ID: "flowseal-fake-tls-auto-alt-discord", Name: "Flowseal FAKE TLS AUTO ALT · Discord Media",
		Description: "FAKE TLS AUTO ALT / Discord Media, FAKE TLS AUTO ALT / Domain TLS, FAKE TLS AUTO ALT / IP TLS.",
		Actions: []string{
			"fake:blob=fake_default_tls:tls_mod=rnd,dupsid,sni=www.google.com:tcp_seq=2:tcp_ack=-66000:tcp_ts_up:repeats=8",
			"fakedsplit:pos=1:tcp_seq=2:tcp_ack=-66000:tcp_ts_up:repeats=8",
		},
	},
	{ID: "flowseal-fake-tls-auto-alt-youtube", Name: "Flowseal FAKE TLS AUTO ALT · Youtube",
		Description: "FAKE TLS AUTO ALT / Youtube.",
		Actions: []string{
			"fake:blob=fake_default_tls:tls_mod=rnd,dupsid,sni=www.google.com:tcp_seq=2:tcp_ack=-66000:tcp_ts_up:ip_id=zero:repeats=8",
			"fakedsplit:pos=1:tcp_seq=2:tcp_ack=-66000:tcp_ts_up:ip_id=zero:repeats=8",
		},
	},
	{ID: "flowseal-fake-tls-auto-alt2-discord", Name: "Flowseal FAKE TLS AUTO ALT2 · Discord Media",
		Description: "FAKE TLS AUTO ALT2 / Discord Media, FAKE TLS AUTO ALT2 / Domain TLS, FAKE TLS AUTO ALT2 / IP TLS.",
		Actions: []string{
			"fake:blob=fake_default_tls:tls_mod=rnd,dupsid,sni=www.google.com:tcp_seq=10000000:tcp_ack=-66000:repeats=8",
			"multisplit:pos=1:seqovl=681:seqovl_pattern=flowseal_tls_google",
		},
	},
	{ID: "flowseal-fake-tls-auto-alt2-youtube", Name: "Flowseal FAKE TLS AUTO ALT2 · Youtube",
		Description: "FAKE TLS AUTO ALT2 / Youtube.",
		Actions: []string{
			"fake:blob=fake_default_tls:tls_mod=rnd,dupsid,sni=www.google.com:tcp_seq=10000000:tcp_ack=-66000:ip_id=zero:repeats=8",
			"multisplit:pos=1:seqovl=681:seqovl_pattern=flowseal_tls_google:ip_id=zero",
		},
	},
	{ID: "flowseal-fake-tls-auto-alt3-discord", Name: "Flowseal FAKE TLS AUTO ALT3 · Discord Media",
		Description: "FAKE TLS AUTO ALT3 / Discord Media, FAKE TLS AUTO ALT3 / Domain TLS, FAKE TLS AUTO ALT3 / IP TLS.",
		Actions: []string{
			"fake:blob=fake_default_tls:tls_mod=rnd,dupsid,sni=www.google.com:tcp_ts=-600000:repeats=8",
			"multisplit:pos=1:seqovl=681:seqovl_pattern=flowseal_tls_google",
		},
	},
	{ID: "flowseal-fake-tls-auto-alt3-youtube", Name: "Flowseal FAKE TLS AUTO ALT3 · Youtube",
		Description: "FAKE TLS AUTO ALT3 / Youtube.",
		Actions: []string{
			"fake:blob=fake_default_tls:tls_mod=rnd,dupsid,sni=www.google.com:tcp_ts=-600000:ip_id=zero:repeats=8",
			"multisplit:pos=1:seqovl=681:seqovl_pattern=flowseal_tls_google:ip_id=zero",
		},
	},
	{ID: "flowseal-fake-tls-auto-alt4-discord", Name: "Flowseal FAKE TLS AUTO ALT4 · Discord Media",
		Description: "FAKE TLS AUTO ALT4 / Discord Media, FAKE TLS AUTO ALT4 / Youtube.",
		Actions: []string{
			"fake:blob=0x00000000:tcp_seq=-10000:tcp_ack=-66000:ip_id=zero:repeats=6",
			"fake:blob=fake_default_tls:tls_mod=rnd,dupsid,sni=www.google.com:tcp_seq=-10000:tcp_ack=-66000:ip_id=zero:repeats=5",
			"multisplit:pos=sld,sld+3,endhost-1:seqovl=600:seqovl_pattern=flowseal_tls_google3:ip_id=zero",
		},
	},
	{ID: "flowseal-fake-tls-auto-alt4-domain", Name: "Flowseal FAKE TLS AUTO ALT4 · Domain TLS",
		Description: "FAKE TLS AUTO ALT4 / Domain TLS, FAKE TLS AUTO ALT4 / IP TLS, FAKE TLS AUTO / Discord Media, FAKE TLS AUTO / Domain TLS, FAKE TLS AUTO / IP TLS.",
		Actions: []string{
			"fake:blob=0x00000000:tcp_seq=-10000:tcp_ack=-66000:repeats=11",
			"fake:blob=fake_default_tls:tls_mod=rnd,dupsid,sni=www.google.com:tcp_seq=-10000:tcp_ack=-66000:repeats=11",
			"multidisorder:pos=1,midsld",
		},
	},
	{ID: "flowseal-fake-tls-auto-youtube", Name: "Flowseal FAKE TLS AUTO · Youtube",
		Description: "FAKE TLS AUTO / Youtube.",
		Actions: []string{
			"fake:blob=0x00000000:tcp_seq=-10000:tcp_ack=-66000:ip_id=zero:repeats=11",
			"fake:blob=fake_default_tls:tls_mod=rnd,dupsid,sni=www.google.com:tcp_seq=-10000:tcp_ack=-66000:ip_id=zero:repeats=11",
			"multidisorder:pos=1,midsld:ip_id=zero",
		},
	},
	{ID: "flowseal-simple-fake-alt-discord", Name: "Flowseal SIMPLE FAKE ALT · Discord Media",
		Description: "SIMPLE FAKE ALT / Discord Media.",
		Actions: []string{
			"fake:blob=flowseal_tls_google:tcp_seq=2:tcp_ack=-66000:tcp_ts_up:repeats=6",
		},
	},
	{ID: "flowseal-simple-fake-alt-youtube", Name: "Flowseal SIMPLE FAKE ALT · Youtube",
		Description: "SIMPLE FAKE ALT / Youtube.",
		Actions: []string{
			"fake:blob=flowseal_tls_google:tcp_seq=2:tcp_ack=-66000:tcp_ts_up:ip_id=zero:repeats=6",
		},
	},
	{ID: "flowseal-simple-fake-alt-domain", Name: "Flowseal SIMPLE FAKE ALT · Domain TLS",
		Description: "SIMPLE FAKE ALT / Domain TLS, SIMPLE FAKE ALT / IP TLS.",
		Actions: []string{
			"fake:blob=flowseal_stun2:tcp_seq=2:tcp_ack=-66000:tcp_ts_up:repeats=6",
			"fake:blob=flowseal_tls_google:tcp_seq=2:tcp_ack=-66000:tcp_ts_up:repeats=6",
		},
	},
	{ID: "flowseal-simple-fake-alt2-domain", Name: "Flowseal SIMPLE FAKE ALT2 · Domain TLS",
		Description: "SIMPLE FAKE ALT2 / Domain TLS, SIMPLE FAKE ALT2 / IP TLS.",
		Actions: []string{
			"fake:blob=flowseal_stun2:tcp_ts=-600000:repeats=6",
			"fake:blob=flowseal_tls_sferum:tcp_ts=-600000:repeats=6",
		},
	},
	{ID: "flowseal-general-domain", Name: "Flowseal GENERAL · Domain TLS",
		Description: "GENERAL / Domain TLS, GENERAL / IP TLS.",
		Actions: []string{
			"multisplit:pos=1:seqovl=568:seqovl_pattern=flowseal_tls_4pda",
		},
	},
}

// The embedded files are packet payload templates, not executable code.
//
//go:embed flowseal/*.bin
var flowsealPayloads embed.FS

var flowsealBlobArgs = func() []string {
	entries, err := flowsealPayloads.ReadDir("flowseal")
	if err != nil {
		panic(err)
	}
	args := make([]string, 0, len(entries))
	for _, entry := range entries {
		data, err := flowsealPayloads.ReadFile("flowseal/" + entry.Name())
		if err != nil {
			panic(err)
		}
		name := entry.Name()[:len(entry.Name())-len(".bin")]
		args = append(args, "--blob=flowseal_"+name+":0x"+hex.EncodeToString(data))
	}
	return args
}()
