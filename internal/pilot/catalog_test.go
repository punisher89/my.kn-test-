package pilot

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCatalogSignatureExpiryAndRestrictions(t *testing.T) {
	public, private, _ := ed25519.GenerateKey(rand.Reader)
	doc := catalogDocument{Revision: 1, Engine: "nfqws2-v1.0.7", Expires: time.Now().Add(time.Hour), Strategies: []Strategy{{ID: "net-test", Name: "test", Actions: []string{"multisplit:pos=2,midsld"}}}}
	data, _ := json.Marshal(doc)
	sig := ed25519.Sign(private, data)
	if _, err := verifyCatalog(data, sig, public, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := verifyCatalog(append(data, ' '), sig, public, time.Now()); err == nil {
		t.Fatal("tampered catalog accepted")
	}
	if _, err := verifyCatalog(data, sig, public, time.Now().Add(2*time.Hour)); err == nil {
		t.Fatal("expired catalog accepted")
	}
	for _, action := range []string{"os.execute:cmd=reboot", "fake:blob=@/etc/passwd", "fake:blob=fake_default_tls:tcp_md5:repeats=999999", "multisplit:pos=2;reboot"} {
		doc.Strategies[0].Actions = []string{action}
		if validateCatalog(doc.Strategies) == nil {
			t.Fatalf("unsafe action accepted: %s", action)
		}
	}
}

func TestPublishedCatalogNativeValidationPersistenceAndSelectedStrategy(t *testing.T) {
	engine := os.Getenv("PILOT_TEST_ENGINE")
	if engine == "" {
		t.Skip("native engine required")
	}
	data, err := os.ReadFile("../../catalog/strategies.json")
	if err != nil {
		t.Fatal(err)
	}
	sigText, _ := os.ReadFile("../../catalog/strategies.json.sig")
	sig, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sigText)))
	key, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(catalogPublicKey))
	doc, err := verifyCatalog(data, sig, key, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config.json")
	c := DefaultConfig()
	c.EngineDir = engine
	b := &testBackend{}
	m := NewManager(c, path, b, failedProber{}, &LogBuffer{})
	if err = m.acceptCatalog(context.Background(), doc); err != nil {
		t.Fatal(err)
	}
	saved, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if saved.CatalogRevision != doc.Revision || len(saved.Catalog) == 0 {
		t.Fatal("catalog not persisted")
	}
	if len(b.configs) != 0 {
		t.Fatal("catalog download restarted traffic engine")
	}
	saved.Profiles[0].Strategy = saved.Catalog[0].ID
	oldAction := saved.Catalog[0].Actions[0]
	m = NewManager(saved, path, b, failedProber{}, &LogBuffer{})
	doc.Revision++
	doc.Strategies[0].Actions = []string{"multisplit:pos=5,midsld"}
	if err = m.acceptCatalog(context.Background(), doc); err != nil {
		t.Fatal(err)
	}
	kept, _ := strategyFor(m.Config(), saved.Profiles[0].Strategy)
	if kept.Actions[0] != oldAction {
		t.Fatal("selected strategy silently changed")
	}
	doc.Revision = 1
	if err = m.acceptCatalog(context.Background(), doc); err == nil {
		t.Fatal("catalog downgrade accepted")
	}
}
