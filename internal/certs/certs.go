package certs

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"log/slog"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

type Bundle struct {
	CAPEM   []byte
	CertPEM []byte
	KeyPEM  []byte
}

const (
	caValidity     = 10 * 365 * 24 * time.Hour
	serverValidity = 825 * 24 * time.Hour
	renewBefore    = 30 * 24 * time.Hour
)

func LocalIPv4s() []net.IP {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		slog.Error("fail, 枚举本机网卡地址失败", "err", err)
		return nil
	}
	var ips []net.IP
	for _, a := range addrs {
		ipNet, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		ip := ipNet.IP.To4()
		if ip == nil || ip.IsLoopback() {
			continue
		}
		ips = append(ips, ip)
	}
	return ips
}

func Ensure(dir string, ips []net.IP) (*Bundle, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create certs dir: %w", err)
	}
	caCert, caKey, caPEM, err := ensureCA(dir)
	if err != nil {
		return nil, err
	}
	certPEM, keyPEM, err := ensureServer(dir, caCert, caKey, ips)
	if err != nil {
		return nil, err
	}
	return &Bundle{CAPEM: caPEM, CertPEM: certPEM, KeyPEM: keyPEM}, nil
}

func ensureCA(dir string) (*x509.Certificate, *ecdsa.PrivateKey, []byte, error) {
	certPath := filepath.Join(dir, "ca.crt")
	keyPath := filepath.Join(dir, "ca.key")

	certPEM, certErr := os.ReadFile(certPath)
	keyPEM, keyErr := os.ReadFile(keyPath)
	if certErr == nil && keyErr == nil {
		cert, err := parseCert(certPEM)
		key, kerr := parseKey(keyPEM)
		if err == nil && kerr == nil && time.Until(cert.NotAfter) > renewBefore {
			slog.Info("hit, 复用已持久化的自签 CA", "notAfter", cert.NotAfter.Format(time.DateOnly))
			return cert, key, certPEM, nil
		}
		slog.Info("hit, 已存 CA 不可用, 重新生成", "certErr", err, "keyErr", kerr)
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("generate ca key: %w", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("ca serial: %w", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "phoneMic CA", Organization: []string{"phoneMic"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(caValidity),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("create ca cert: %w", err)
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("marshal ca key: %w", err)
	}
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := writeFiles(certPath, certPEM, keyPath, keyPEM); err != nil {
		return nil, nil, nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("parse new ca cert: %w", err)
	}
	slog.Info("done, 已生成新的自签 CA 并持久化", "dir", dir, "notAfter", cert.NotAfter.Format(time.DateOnly))
	return cert, key, certPEM, nil
}

func ensureServer(dir string, caCert *x509.Certificate, caKey *ecdsa.PrivateKey, ips []net.IP) ([]byte, []byte, error) {
	certPath := filepath.Join(dir, "server.crt")
	keyPath := filepath.Join(dir, "server.key")

	certPEM, certErr := os.ReadFile(certPath)
	keyPEM, keyErr := os.ReadFile(keyPath)
	if certErr == nil && keyErr == nil {
		cert, err := parseCert(certPEM)
		_, kerr := parseKey(keyPEM)
		if err == nil && kerr == nil &&
			time.Until(cert.NotAfter) > renewBefore &&
			caCert.CheckSignature(cert.SignatureAlgorithm, cert.RawTBSCertificate, cert.Signature) == nil &&
			coversIPs(cert, ips) {
			slog.Info("hit, 复用服务证书", "sanIPs", len(cert.IPAddresses), "notAfter", cert.NotAfter.Format(time.DateOnly))
			return certPEM, keyPEM, nil
		}
		slog.Info("hit, 服务证书需重新签发", "ipCount", len(ips), "certErr", err, "keyErr", kerr)
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generate server key: %w", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, fmt.Errorf("server serial: %w", err)
	}
	sanIPs := append([]net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}, ips...)
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "phoneMic", Organization: []string{"phoneMic"}},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(serverValidity),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  sanIPs,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
	if err != nil {
		return nil, nil, fmt.Errorf("create server cert: %w", err)
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal server key: %w", err)
	}
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := writeFiles(certPath, certPEM, keyPath, keyPEM); err != nil {
		return nil, nil, err
	}
	slog.Info("done, 已签发服务证书", "sanIPs", len(sanIPs))
	return certPEM, keyPEM, nil
}

func coversIPs(cert *x509.Certificate, ips []net.IP) bool {
	for _, want := range ips {
		found := false
		for _, have := range cert.IPAddresses {
			if have.Equal(want) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func parseCert(pemBytes []byte) (*x509.Certificate, error) {
	blk, _ := pem.Decode(pemBytes)
	if blk == nil {
		return nil, fmt.Errorf("invalid cert pem")
	}
	return x509.ParseCertificate(blk.Bytes)
}

func parseKey(pemBytes []byte) (*ecdsa.PrivateKey, error) {
	blk, _ := pem.Decode(pemBytes)
	if blk == nil {
		return nil, fmt.Errorf("invalid key pem")
	}
	return x509.ParseECPrivateKey(blk.Bytes)
}

func writeFiles(certPath string, certPEM []byte, keyPath string, keyPEM []byte) error {
	if err := os.WriteFile(certPath, certPEM, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", certPath, err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", keyPath, err)
	}
	return nil
}