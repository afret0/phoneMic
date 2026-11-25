package server

import (
	"log/slog"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/afret0/phoneMic/internal/certs"
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
	host, err := os.Hostname()
	if err != nil || host == "" {
		slog.Error("fail, 读取主机名失败, mDNS 主机名回退实例名", "err", err)
		host = name
	} else {
		name = "phoneMic-" + strings.TrimSuffix(host, ".local")
	}
	reachable := certs.ReachableIPv4s()
	if len(reachable) == 0 {
		slog.Error("fail, 无可广播的局域网地址, app 自动发现不可用, 可手动输 IP 连接")
		return nil
	}
	ip := reachable[0].String()
	slog.Info("hit, mDNS 只广播排序首位地址, 避免手机连到虚拟网卡", "advertise", ip, "candidates", len(reachable))
	entry, err := zeroconf.RegisterProxy(name, mdnsService, "local.", port, host, []string{ip}, []string{"v=1", "path=/ws", "http=" + httpPortStr}, nil)
	if err != nil {
		slog.Error("fail, mDNS 注册失败, app 自动发现不可用, 可手动输 IP 连接", "err", err)
		return nil
	}
	slog.Info("done, mDNS 已注册", "name", name, "service", mdnsService, "port", port, "advertise", ip)
	return entry
}
