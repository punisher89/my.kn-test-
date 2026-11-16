package pilot

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDomainSourceValidationAndSignedRegistry(t *testing.T) {
	for _, s := range []string{"http://example.com/x", "https://127.0.0.1/x", "https://localhost/x", "https://example.com:8443/x", "https://user:pass@example.com/x", "https://example.com/x?token=secret"} {
		if sourceURL(s) == nil {
			t.Fatal("unsafe URL accepted", s)
		}
	}
	client := domainHTTPClient()
	defer client.CloseIdleConnections()
	_, dialError := client.Transport.(*http.Transport).DialContext(context.Background(), "tcp", "127.0.0.1:443")
	if dialError == nil {
		t.Fatal("private source address accepted")
	}
	for _, b := range []string{"", "<html>blocked</html>", "127.0.0.1", "example.com\ninvalid"} {
		if _, e := parseDomainText([]byte(b)); e == nil {
			t.Fatal("invalid source accepted")
		}
	}
	d, e := parseDomainText([]byte("# comment\nEXAMPLE.COM\n*.cdn.example.com\nexample.com\n"))
	if e != nil || len(d) != 2 {
		t.Fatal("valid source rejected", e)
	}
	data, e := os.ReadFile("../../catalog/domains.json")
	if e != nil {
		t.Fatal(e)
	}
	sig, e := os.ReadFile("../../catalog/domains.json.sig")
	if e != nil {
		t.Fatal(e)
	}
	var doc domainRegistry
	if e = signedJSON(data, sig, updatesPublicKey, &doc); e != nil || doc.Kind != "pilot-domains" || len(doc.Services) != 9 {
		t.Fatal("bundled registry signature invalid", e)
	}
	data[0] ^= 1
	if signedJSON(data, sig, updatesPublicKey, &doc) == nil {
		t.Fatal("tampered registry accepted")
	}
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	b, _ := json.Marshal(domainRegistry{Kind: "pilot-domains", Revision: 1})
	signature := []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(priv, b)))
	if e = signedJSON(b, signature, base64.StdEncoding.EncodeToString(pub), &doc); e != nil {
		t.Fatal(e)
	}
}
func TestDomainRefreshPreservesLocalAndRejectsConflicts(t *testing.T) {
	old := readDomainSource
	defer func() { readDomainSource = old }()
	body := []byte("cdn.example.com\nblocked.example.com\n")
	readDomainSource = func(context.Context, string) ([]byte, error) { return body, nil }
	c := DefaultConfig()
	c.Enabled = true
	c.Profiles[0].DomainSource = "https://lists.example.com/youtube.txt"
	c.Profiles[0].SourceExclusions = []string{"blocked.example.com"}
	c.Profiles[0].Domains = append(c.Profiles[0].Domains, "local.example.com")
	backend := &testBackend{running: true}
	m := NewManager(c, filepath.Join(t.TempDir(), "config.json"), backend, successfulProber{}, &LogBuffer{})
	if e := m.UpdateServiceDomains(t.Context(), "youtube"); e != nil {
		t.Fatal(e)
	}
	got := m.Config()
	joined := strings.Join(got.Profiles[0].Domains, " ")
	if !strings.Contains(joined, "cdn.example.com") || !strings.Contains(joined, "local.example.com") || strings.Contains(joined, "blocked.example.com") || got.Profiles[0].DomainsUpdated.IsZero() {
		t.Fatal("local domains or source exclusions lost")
	}
	calls := len(backend.configs)
	if e := m.UpdateServiceDomains(t.Context(), "youtube"); e != nil || len(backend.configs) != calls {
		t.Fatal("unchanged list restarted engine", e)
	}
	before := strings.Join(m.Config().Profiles[0].Domains, " ")
	body = []byte("discord.com\n")
	if m.UpdateServiceDomains(t.Context(), "youtube") == nil || strings.Join(m.Config().Profiles[0].Domains, " ") != before {
		t.Fatal("conflicting source replaced configuration")
	}
	body = []byte("new.example.com\n")
	cfg := m.Config()
	cfg.Profiles[0].DomainSHA256 = strings.Repeat("0", 64)
	if e := m.Change(cfg); e != nil {
		t.Fatal(e)
	}
	if m.UpdateServiceDomains(t.Context(), "youtube") == nil {
		t.Fatal("bad checksum accepted")
	}
	sum := sha256.Sum256(body)
	cfg = m.Config()
	cfg.Profiles[0].DomainSHA256 = hex.EncodeToString(sum[:])
	if e := m.Change(cfg); e != nil {
		t.Fatal(e)
	}
	if e := m.UpdateServiceDomains(t.Context(), "youtube"); e != nil {
		t.Fatal(e)
	}
	m.domainCache = &domainRegistry{Kind: "pilot-domains", Revision: 2, Services: map[string][]string{"youtube": {"trusted.example.com"}}}
	m.domainCacheAt = time.Now()
	cfg = m.Config()
	cfg.Profiles[0].DomainSource = "pilot:youtube"
	cfg.Profiles[0].DomainSHA256 = ""
	if e := m.Change(cfg); e != nil {
		t.Fatal(e)
	}
	if e := m.UpdateServiceDomains(t.Context(), "youtube"); e != nil {
		t.Fatal(e)
	}
	if m.Config().Profiles[0].DomainRevision != 2 {
		t.Fatal("domain revision missing")
	}
}
func TestCustomServiceContentChecksAndPersistence(t *testing.T) {
	m := NewManager(DefaultConfig(), filepath.Join(t.TempDir(), "config.json"), &testBackend{}, successfulProber{}, &LogBuffer{})
	p := Profile{Name: "Свой сервис", Domains: []string{"custom.example.com"}, Targets: []Target{{URL: "https://custom.example.com/", Status: 200}}}
	if m.AddCustomService(p) == nil {
		t.Fatal("empty control fragment accepted")
	}
	p.Targets[0].Contains = "Expected"
	if e := m.AddCustomService(p); e != nil {
		t.Fatal(e)
	}
	got := m.Config().Profiles[2]
	if !strings.HasPrefix(got.ID, "custom-") || got.Strategy != "none" || got.Name != p.Name {
		t.Fatal("custom profile wrong")
	}
}

func TestDomainParentCanonicalizationPreservesCoverage(t *testing.T) {
	old := readDomainSource
	defer func() { readDomainSource = old }()
	readDomainSource = func(context.Context, string) ([]byte, error) { return []byte("example.com\n"), nil }
	c := DefaultConfig()
	c.Profiles[0].Domains = append(c.Profiles[0].Domains, "manual.example.com")
	c.Profiles[0].DomainSource = "https://lists.example.net/list.txt"
	m := NewManager(c, filepath.Join(t.TempDir(), "config.json"), &testBackend{}, successfulProber{}, &LogBuffer{})
	if e := m.UpdateServiceDomains(t.Context(), "youtube"); e != nil {
		t.Fatal(e)
	}
	domains := m.Config().Profiles[0].Domains
	covered := false
	for _, d := range domains {
		if hostMatches("manual.example.com", d) {
			covered = true
		}
	}
	if !covered || strings.Contains(strings.Join(domains, " "), "manual.example.com") {
		t.Fatal("parent did not canonicalize while preserving scope")
	}
}
