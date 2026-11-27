package pilot

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"errors"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

const HTTPSPanelPort = "9443"

type panelCertificate struct {
	Certificate []byte
	PrivateKey  []byte
}

// PanelTLSConfig installs a persistent local certificate limited to the panel IP.
func PanelTLSConfig(dir, host string) (*tls.Config, error) {
	ip := net.ParseIP(host)
	if ip == nil || (!ip.IsPrivate() && !ip.IsLoopback()) {
		return nil, errors.New("некорректный локальный адрес HTTPS")
	}
	path := filepath.Join(dir, "panel-tls.private.json")
	var saved panelCertificate
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &saved); err != nil {
			return nil, errors.New("локальный сертификат Pilot повреждён")
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	now := time.Now()
	cert, _ := x509.ParseCertificate(saved.Certificate)
	if cert == nil || cert.VerifyHostname(host) != nil || !cert.NotAfter.After(now.AddDate(0, 0, 7)) {
		serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
		if err != nil {
			return nil, err
		}
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return nil, err
		}
		template := x509.Certificate{
			SerialNumber: serial, Subject: pkix.Name{CommonName: "Keenetic Pilot"},
			NotBefore: now.Add(-time.Hour), NotAfter: now.AddDate(1, 0, 0),
			KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
			IPAddresses: []net.IP{ip},
		}
		saved.Certificate, err = x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
		if err != nil {
			return nil, err
		}
		saved.PrivateKey, err = x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			return nil, err
		}
		if err := WriteJSON(path, saved); err != nil {
			return nil, err
		}
	}
	key, err := x509.ParsePKCS8PrivateKey(saved.PrivateKey)
	if err != nil {
		return nil, err
	}
	return &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{
		{Certificate: [][]byte{saved.Certificate}, PrivateKey: key},
	}}, nil
}
