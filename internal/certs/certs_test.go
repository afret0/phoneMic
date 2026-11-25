package certs

import (
	"crypto/x509"
	"net"
	"testing"
	"time"
)

func mustEnsure(t *testing.T, dir string, ips []net.IP) *Bundle {
	t.Helper()
	b, err := Ensure(dir, ips)
	if err != nil {
		t.Fatalf("Ensure err: %s", err)
	}
	if len(b.CAPEM) == 0 || len(b.CertPEM) == 0 || len(b.KeyPEM) == 0 {
		t.Fatal("bundle 存在空 PEM")
	}
	return b
}

func TestEnsure_生成与复用(t *testing.T) {
	dir := t.TempDir()
	ips := []net.IP{net.ParseIP("192.168.1.10")}
	first := mustEnsure(t, dir, ips)
	second := mustEnsure(t, dir, ips)
	if string(first.CAPEM) != string(second.CAPEM) || string(first.CertPEM) != string(second.CertPEM) {
		t.Fatal("同目录下二次 Ensure 应原样复用 CA 与服务证书")
	}

	cert, err := parseCert(first.CertPEM)
	if err != nil {
		t.Fatalf("parseCert err: %s", err)
	}
	ca, err := parseCert(first.CAPEM)
	if err != nil {
		t.Fatalf("parseCert ca err: %s", err)
	}
	if err := ca.CheckSignature(cert.SignatureAlgorithm, cert.RawTBSCertificate, cert.Signature); err != nil {
		t.Fatalf("服务证书未被 CA 签名, err: %s", err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	if _, err := cert.Verify(x509.VerifyOptions{
		Roots:     pool,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSName:   "localhost",
	}); err != nil {
		t.Fatalf("证书链校验失败, err: %s", err)
	}
	if !coversIPs(cert, ips) {
		t.Fatal("SAN 未覆盖本机 IP")
	}
	if !coversIPs(cert, []net.IP{net.ParseIP("127.0.0.1")}) {
		t.Fatal("SAN 未覆盖 127.0.0.1")
	}
	if time.Until(cert.NotAfter) <= renewBefore {
		t.Fatal("新证书有效期不足续期阈值")
	}
}

func TestEnsure_IP变化重签且CA不变(t *testing.T) {
	dir := t.TempDir()
	first := mustEnsure(t, dir, []net.IP{net.ParseIP("192.168.1.10")})
	second := mustEnsure(t, dir, []net.IP{net.ParseIP("192.168.1.10"), net.ParseIP("10.0.0.8")})
	if string(first.CAPEM) != string(second.CAPEM) {
		t.Fatal("IP 变化不应重新生成 CA(手机端已信任)")
	}
	if string(first.CertPEM) == string(second.CertPEM) {
		t.Fatal("IP 变化后服务证书应重签")
	}
	cert, err := parseCert(second.CertPEM)
	if err != nil {
		t.Fatalf("parseCert err: %s", err)
	}
	if !coversIPs(cert, []net.IP{net.ParseIP("10.0.0.8")}) {
		t.Fatal("重签后 SAN 未覆盖新增 IP")
	}
}

func TestEnsure_无局域网IP(t *testing.T) {
	b := mustEnsure(t, t.TempDir(), nil)
	cert, err := parseCert(b.CertPEM)
	if err != nil {
		t.Fatalf("parseCert err: %s", err)
	}
	if !coversIPs(cert, []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}) {
		t.Fatal("无局域网 IP 时也应覆盖回环地址")
	}
}