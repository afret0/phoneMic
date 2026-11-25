package certs

import (
	"crypto/x509"
	"net"
	"strings"
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

func TestReachableSorted(t *testing.T) {
	ips := func(ss ...string) []net.IP {
		var out []net.IP
		for _, s := range ss {
			out = append(out, net.ParseIP(s))
		}
		return out
	}
	join := func(l []net.IP) string {
		var ss []string
		for _, ip := range l {
			ss = append(ss, ip.String())
		}
		return strings.Join(ss, ",")
	}
	cases := []struct {
		name   string
		in     []net.IP
		egress net.IP
		want   string
	}{
		{"剔除链路本地", ips("169.254.245.108", "192.168.31.126"), nil, "192.168.31.126"},
		{"全部链路本地返回空", ips("169.254.1.1", "169.254.2.2"), nil, ""},
		{"无出口时 192.168 兜底再私网再公网", ips("198.18.0.1", "10.0.0.8", "192.168.31.126", "172.16.5.2"), nil, "192.168.31.126,10.0.0.8,172.16.5.2,198.18.0.1"},
		{"172.31 私网 172.32 非私网", ips("172.32.0.1", "172.31.0.1"), nil, "172.31.0.1,172.32.0.1"},
		{"CGNAT 段不算私网", ips("100.64.0.1", "192.168.1.5"), nil, "192.168.1.5,100.64.0.1"},
		{"公网保持原序", ips("8.8.8.8", "1.1.1.1"), nil, "8.8.8.8,1.1.1.1"},
		{"空输入", nil, nil, ""},
		{"出口 IP 置顶压过 Docker 网段", ips("172.17.0.1", "172.24.16.1", "192.168.31.126"), net.ParseIP("192.168.31.126"), "192.168.31.126,172.17.0.1,172.24.16.1"},
		{"出口 IP 不在地址列表不影响兜底", ips("172.17.0.1", "192.168.1.5"), net.ParseIP("192.168.31.126"), "192.168.1.5,172.17.0.1"},
		{"出口是 10 段也置顶", ips("192.168.1.5", "10.0.0.8"), net.ParseIP("10.0.0.8"), "10.0.0.8,192.168.1.5"},
		{"同档保持接口原序", ips("192.168.2.2", "192.168.1.1"), nil, "192.168.2.2,192.168.1.1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := join(reachableSorted(c.in, c.egress)); got != c.want {
				t.Fatalf("reachableSorted = %q, want %q", got, c.want)
			}
		})
	}
}
