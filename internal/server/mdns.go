package server

import (
	"log/slog"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/grandcat/zeroconf"
)

const mdnsService = "_phonemic._tcp"

func (s *Server) registerMDNS() *zeroconf.Server {
	_, portStr, err := net.SplitHostPort(s.httpsAddr)
	if err != nil {
		slog.Error("fail, mDNS 端口解析失败, app 自动发现不可用", "addr", s.httpsAddr, "err", err)
		return nil
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		slog.Error("fail, mDNS 端口非法, app 自动发现不可用", "port", portStr, "err", err)
		return nil
	}
	_, httpPortStr, err := net.SplitHostPort(s.httpAddr)
	if err != nil {
		slog.Error("fail, mDNS HTTP 端口解析失败, app 首次固定证书需手动输端口", "addr", s.httpAddr, "err", err)
		httpPortStr = "8080"
	}
	name := "phoneMic"
	if hn, err := os.Hostname(); err == nil && hn != "" {
		name = "phoneMic-" + strings.TrimSuffix(hn, ".local")
	}
	entry, err := zeroconf.Register(name, mdnsService, "local.", port, []string{"v=1", "path=/ws", "http=" + httpPortStr}, nil)
	if err != nil {
		slog.Error("fail, mDNS 注册失败, app 自动发现不可用, 可手动输 IP 连接", "err", err)
		return nil
	}
	slog.Info("done, mDNS 已注册", "name", name, "service", mdnsService, "port", port)
	return entry
}
