package pilot

import (
	"bytes"
	"context"
	"crypto/ed25519"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const catalogURL = "https://raw.githubusercontent.com/punisher89/my.kn-test-/refs/heads/feature/keenetic-pilot/catalog/strategies.json"

//go:embed catalog-public.key
var catalogPublicKey string

type catalogDocument struct {
	Revision   uint64     `json:"revision"`
	Engine     string     `json:"engine"`
	Expires    time.Time  `json:"expires"`
	Strategies []Strategy `json:"strategies"`
}

var actionPatterns = []*regexp.Regexp{
	regexp.MustCompile(`^(multisplit|multidisorder|fakedsplit):pos=(1|2|3|5|10|host|endhost|midsld)(,(1|2|3|5|10|host|endhost|midsld)){0,5}(:tcp_md5)?$`),
	regexp.MustCompile(`^fake:blob=fake_default_tls:(tcp_md5|tcp_seq=-10000|ip_autottl=-1,3-20:ip6_autottl=-1,3-20):repeats=([1-9]|1[0-6])$`),
}

func validateCatalog(pool []Strategy) error {
	if len(pool) > 64 {
		return errors.New("каталог: не более 64 вариантов")
	}
	ids := map[string]bool{}
	for _, s := range pool {
		if !strings.HasPrefix(s.ID, "net-") || !idRE.MatchString(s.ID) || ids[s.ID] || len(s.Name) == 0 || len(s.Name) > 120 || len(s.Description) > 500 || len(s.Actions) == 0 || len(s.Actions) > 3 {
			return errors.New("некорректная запись каталога")
		}
		ids[s.ID] = true
		for _, action := range s.Actions {
			ok := false
			for _, pattern := range actionPatterns {
				if pattern.MatchString(action) {
					ok = true
				}
			}
			if !ok {
				return fmt.Errorf("неподдерживаемое действие каталога: %q", action)
			}
		}
	}
	return nil
}
func verifyCatalog(data, signature, key []byte, now time.Time) (catalogDocument, error) {
	var doc catalogDocument
	if len(key) != ed25519.PublicKeySize || !ed25519.Verify(key, data, signature) {
		return doc, errors.New("неверная подпись каталога")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&doc); err != nil {
		return doc, err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return doc, errors.New("лишние данные каталога")
	}
	if doc.Revision == 0 || doc.Engine != "nfqws2-v1.0.7" || !doc.Expires.After(now) {
		return doc, errors.New("каталог устарел или несовместим с движком")
	}
	return doc, validateCatalog(doc.Strategies)
}
func fetchCatalog(ctx context.Context, url string, token string) ([]byte, error) {
	client := &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) > 3 || r.URL.Scheme != "https" || r.URL.Host != "raw.githubusercontent.com" {
			return errors.New("недопустимое перенаправление")
		}
		return nil
	}}
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "token "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("каталог: HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 131073))
	if len(data) > 131072 {
		return nil, errors.New("каталог слишком большой")
	}
	return data, err
}
func (m *Manager) UpdateCatalog(ctx context.Context) error {
	if !m.op.TryLock() {
		return ErrBusy
	}
	defer m.op.Unlock()
	return m.updateCatalog(ctx)
}
func (m *Manager) updateCatalog(ctx context.Context) error {
	token := ""
	if b, err := os.ReadFile(filepath.Join(filepath.Dir(m.path), "github-token")); err == nil {
		token = strings.TrimSpace(string(b))
		if strings.ContainsAny(token, "\r\n\x00") {
			return errors.New("некорректный файл токена GitHub")
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	data, err := fetchCatalog(ctx, catalogURL, token)
	if err != nil {
		return err
	}
	sigText, err := fetchCatalog(ctx, catalogURL+".sig", token)
	if err != nil {
		return err
	}
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sigText)))
	if err != nil {
		return err
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(catalogPublicKey))
	if err != nil {
		return err
	}
	doc, err := verifyCatalog(data, sig, key, time.Now())
	if err != nil {
		return err
	}
	return m.acceptCatalog(ctx, doc)
}
func (m *Manager) acceptCatalog(ctx context.Context, doc catalogDocument) error {
	c := m.Config()
	if doc.Revision < c.CatalogRevision {
		return errors.New("отклонена старая ревизия каталога")
	}
	if doc.Revision == c.CatalogRevision {
		return nil
	}
	selected := map[string]bool{}
	for _, p := range c.Profiles {
		selected[p.Strategy] = true
	}
	pool := []Strategy{}
	for _, old := range c.Catalog {
		if selected[old.ID] {
			pool = append(pool, old)
		}
	}
	for _, s := range doc.Strategies {
		if !selected[s.ID] {
			pool = append(pool, s)
		} else {
			found := false
			for _, old := range pool {
				if old.ID == s.ID {
					found = true
				}
			}
			if !found {
				pool = append(pool, s)
			}
		}
	}
	c.Catalog = pool
	c.CatalogRevision = doc.Revision
	if err := c.Validate(); err != nil {
		return err
	}
	for _, s := range doc.Strategies {
		if err := ctx.Err(); err != nil {
			return err
		}
		check := cloneConfig(c)
		check.Profiles[0].Strategy = s.ID
		if err := CheckEngine(check); err != nil {
			return err
		}
	}
	if err := m.save(c); err != nil {
		return err
	}
	m.log.Add(fmt.Sprintf("Каталог обновлён: ревизия %d, %d вариантов; активные стратегии сохранены", doc.Revision, len(pool)))
	return nil
}

func ImportCatalog(path, dataPath string) error {
	c, err := LoadConfig(path)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(dataPath)
	if err != nil {
		return err
	}
	text, err := os.ReadFile(dataPath + ".sig")
	if err != nil {
		return err
	}
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(text)))
	if err != nil {
		return err
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(catalogPublicKey))
	if err != nil {
		return err
	}
	doc, err := verifyCatalog(data, sig, key, time.Now())
	if err != nil {
		return err
	}
	m := NewManager(c, path, Development{}, HTTPProber{}, &LogBuffer{})
	return m.acceptCatalog(context.Background(), doc)
}
