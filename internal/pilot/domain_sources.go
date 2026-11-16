package pilot

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

//go:embed updates-public.key
var updatesPublicKey string

const domainRegistryURL = "https://raw.githubusercontent.com/punisher89/my.kn-test-/refs/heads/feature/keenetic-pilot/catalog/domains.json"

type domainRegistry struct {
	Kind     string              `json:"kind"`
	Revision uint64              `json:"revision"`
	Services map[string][]string `json:"services"`
}

func signedJSON(data, sigText []byte, public string, out any) error {
	key, e := base64.StdEncoding.DecodeString(strings.TrimSpace(public))
	if e != nil || len(key) != ed25519.PublicKeySize {
		return errors.New("некорректный ключ подписи")
	}
	sig, e := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sigText)))
	if e != nil || !ed25519.Verify(key, data, sig) {
		return errors.New("неверная подпись")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if e = d.Decode(out); e != nil {
		return e
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("лишние данные документа")
	}
	return nil
}
func githubReadToken(path string) (string, error) {
	b, e := os.ReadFile(filepath.Join(filepath.Dir(path), "github-token"))
	if os.IsNotExist(e) {
		return "", nil
	}
	if e != nil {
		return "", e
	}
	s := strings.TrimSpace(string(b))
	if strings.ContainsAny(s, "\r\n\x00") {
		return "", errors.New("некорректный токен GitHub")
	}
	return s, nil
}
func sourceURL(raw string) error {
	if len(raw) > 2048 {
		return errors.New("ссылка на источник слишком длинная")
	}
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" || u.RawQuery != "" || u.Port() != "" || !ValidDomain(u.Hostname()) {
		return errors.New("источник: нужен публичный HTTPS URL без порта, параметров и пароля")
	}
	return nil
}
func domainHTTPClient() *http.Client {
	t := &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		h, p, e := net.SplitHostPort(address)
		if e != nil {
			return nil, e
		}
		ips, e := net.DefaultResolver.LookupIP(ctx, "ip", h)
		if e != nil {
			return nil, e
		}
		if len(ips) == 0 {
			return nil, errors.New("DNS: адресов нет")
		}
		for _, ip := range ips {
			if !publicIP(ip) {
				return nil, errors.New("источник вернул непубличный IP")
			}
		}
		var last error
		d := net.Dialer{Timeout: 10 * time.Second}
		for _, ip := range ips {
			c, e := d.DialContext(ctx, network, net.JoinHostPort(ip.String(), p))
			if e == nil {
				return c, nil
			}
			last = e
		}
		return nil, last
	}}
	return &http.Client{Transport: t, Timeout: 25 * time.Second, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) > 3 {
			return errors.New("слишком много перенаправлений")
		}
		return sourceURL(r.URL.String())
	}}
}
func fetchDomainText(ctx context.Context, raw string) ([]byte, error) {
	if e := sourceURL(raw); e != nil {
		return nil, e
	}
	client := domainHTTPClient()
	defer client.CloseIdleConnections()
	req, e := http.NewRequestWithContext(ctx, "GET", raw, nil)
	if e != nil {
		return nil, e
	}
	r, e := client.Do(req)
	if e != nil {
		return nil, e
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		return nil, fmt.Errorf("источник: HTTP %d", r.StatusCode)
	}
	b, e := io.ReadAll(io.LimitReader(r.Body, 131073))
	if e != nil {
		return nil, e
	}
	if len(b) > 131072 {
		return nil, errors.New("список доменов слишком большой")
	}
	return b, nil
}
func parseDomainText(b []byte) ([]string, error) {
	out := []string{}
	seen := map[string]bool{}
	for _, line := range strings.Split(string(b), "\n") {
		v := strings.TrimSpace(strings.SplitN(line, "#", 2)[0])
		if v == "" {
			continue
		}
		v = strings.ToLower(strings.TrimPrefix(strings.TrimPrefix(v, "*."), "domain:"))
		if !ValidDomain(v) {
			return nil, fmt.Errorf("источник содержит некорректный домен: %.120s", v)
		}
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
		if len(out) > 300 {
			return nil, errors.New("в списке более 300 доменов")
		}
	}
	if len(out) == 0 {
		return nil, errors.New("источник не содержит доменов")
	}
	return out, nil
}

var readDomainSource = fetchDomainText

func (m *Manager) loadDomainRegistry(ctx context.Context) (*domainRegistry, error) {
	if time.Since(m.domainCacheAt) < time.Minute {
		if m.domainCacheError != nil {
			return nil, m.domainCacheError
		}
		if m.domainCache != nil {
			return m.domainCache, nil
		}
	}
	m.domainCacheAt = time.Now()
	doc, err := func() (*domainRegistry, error) {
		token, e := githubReadToken(m.path)
		if e != nil {
			return nil, e
		}
		data, e := fetchCatalog(ctx, domainRegistryURL, token)
		if e != nil {
			return nil, e
		}
		sig, e := fetchCatalog(ctx, domainRegistryURL+".sig", token)
		if e != nil {
			return nil, e
		}
		var doc domainRegistry
		if e = signedJSON(data, sig, updatesPublicKey, &doc); e != nil {
			return nil, e
		}
		if doc.Kind != "pilot-domains" || doc.Revision < 1 || len(doc.Services) > 64 {
			return nil, errors.New("неверный каталог доменов")
		}
		return &doc, nil
	}()
	m.domainCache, m.domainCacheError = doc, err
	return doc, err
}
func (m *Manager) fetchDomains(ctx context.Context, p Profile) ([]string, error) {
	if strings.HasPrefix(p.DomainSource, "pilot:") {
		doc, e := m.loadDomainRegistry(ctx)
		if e != nil {
			return nil, e
		}
		if doc.Revision < p.DomainRevision {
			return nil, errors.New("устаревший каталог доменов")
		}
		domains, ok := doc.Services[strings.TrimPrefix(p.DomainSource, "pilot:")]
		if !ok {
			return nil, errors.New("сервис не найден в каталоге доменов")
		}
		return parseDomainText([]byte(strings.Join(domains, "\n")))
	}
	b, e := readDomainSource(ctx, p.DomainSource)
	if e != nil {
		return nil, e
	}
	if p.DomainSHA256 != "" {
		sum := sha256.Sum256(b)
		if hex.EncodeToString(sum[:]) != p.DomainSHA256 {
			return nil, errors.New("контрольная сумма списка доменов не совпадает")
		}
	}
	return parseDomainText(b)
}
func (m *Manager) UpdateServiceDomains(ctx context.Context, id string) error {
	if !m.op.TryLock() {
		return ErrBusy
	}
	defer m.op.Unlock()
	return m.updateServiceDomains(ctx, id)
}
func (m *Manager) updateServiceDomains(ctx context.Context, id string) error {
	c := m.Config()
	idx := -1
	for i, p := range c.Profiles {
		if p.ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return errors.New("профиль не найден")
	}
	p := &c.Profiles[idx]
	previousDomains := append([]string{}, p.Domains...)
	if p.DomainSource == "" {
		return errors.New("источник доменов не задан")
	}
	domains, e := m.fetchDomains(ctx, *p)
	if e != nil {
		return e
	}
	for _, d := range domains {
		excluded := false
		for _, x := range p.SourceExclusions {
			if hostMatches(d, x) || hostMatches(x, d) {
				excluded = true
				break
			}
		}
		if excluded {
			continue
		}
		covered := false
		for _, old := range p.Domains {
			if hostMatches(d, old) {
				covered = true
				break
			}
		}
		if covered {
			continue
		}
		// A source parent covers existing children; canonicalize without losing
		// their traffic scope. Cross-profile overlap is checked before apply.
		next := []string{}
		for _, old := range p.Domains {
			if !hostMatches(old, d) {
				next = append(next, old)
			}
		}
		p.Domains = append(next, d)
	}
	p.DomainsUpdated = time.Now().UTC()
	if strings.HasPrefix(p.DomainSource, "pilot:") && m.domainCache != nil {
		p.DomainRevision = m.domainCache.Revision
	}
	// Validation and the existing apply/rollback path protect overlap, target scope,
	// previous configuration and running service. Local domains are never removed.
	if slices.Equal(previousDomains, p.Domains) {
		return m.setError(m.save(c))
	}
	return m.changeLocked(c)
}
