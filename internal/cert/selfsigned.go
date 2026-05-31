// Package cert generates self-signed TLS certificates for use in config templates.
package cert

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

// Bundle holds PEM-encoded TLS material and the fingerprint of the CA cert.
type Bundle struct {
	CACertPEM  []byte
	CertPEM    []byte
	KeyPEM     []byte
	// CAFingerprint is the SHA-256 hex fingerprint of CACertPEM (no colons).
	CAFingerprint string
}

// Generate creates a self-signed CA and a leaf cert signed by that CA.
// hosts may contain DNS names and/or IP addresses.
func Generate(hosts []string) (*Bundle, error) {
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("cert: gen CA key: %w", err)
	}

	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "hiddify-health-ca"},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(90 * 24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		return nil, fmt.Errorf("cert: create CA: %w", err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		return nil, err
	}

	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("cert: gen leaf key: %w", err)
	}

	leafTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: firstHost(hosts)},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(90 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			leafTemplate.IPAddresses = append(leafTemplate.IPAddresses, ip)
		} else {
			leafTemplate.DNSNames = append(leafTemplate.DNSNames, h)
		}
	}

	leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		return nil, fmt.Errorf("cert: create leaf: %w", err)
	}

	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER})
	keyDER, err := x509.MarshalECPrivateKey(leafKey)
	if err != nil {
		return nil, err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	fp := sha256.Sum256(caDER)
	fingerprint := hex.EncodeToString(fp[:])

	return &Bundle{
		CACertPEM:     caPEM,
		CertPEM:       certPEM,
		KeyPEM:        keyPEM,
		CAFingerprint: fingerprint,
	}, nil
}

// WriteToDir writes ca.pem, cert.pem, key.pem into dir and returns their paths.
func WriteToDir(b *Bundle, dir string) (caPath, certPath, keyPath string, err error) {
	if err = os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	caPath = filepath.Join(dir, "ca.pem")
	certPath = filepath.Join(dir, "cert.pem")
	keyPath = filepath.Join(dir, "key.pem")
	if err = os.WriteFile(caPath, b.CACertPEM, 0o600); err != nil {
		return
	}
	if err = os.WriteFile(certPath, b.CertPEM, 0o600); err != nil {
		return
	}
	err = os.WriteFile(keyPath, b.KeyPEM, 0o600)
	return
}

// TLSConfig returns a *tls.Config that uses this bundle's cert/key.
func (b *Bundle) TLSConfig() (*tls.Config, error) {
	cert, err := tls.X509KeyPair(b.CertPEM, b.KeyPEM)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(b.CACertPEM)
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      pool,
		MinVersion:   tls.VersionTLS12,
	}, nil
}

func firstHost(hosts []string) string {
	if len(hosts) > 0 {
		return hosts[0]
	}
	return "localhost"
}
