package server

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/afret0/phoneMic/internal/audio"
	"github.com/afret0/phoneMic/internal/certs"
	"github.com/afret0/phoneMic/web"
	"github.com/gorilla/websocket"
)

const (
	wsReadLimit    = 64 * 1024
	wsPongTimeout  = 30 * time.Second
	wsPingInterval = 10 * time.Second
	kickCloseCode  = 4001
)

type session struct {
	conn      *websocket.Conn
	remote    string
	bytes     atomic.Uint64
	frames    atomic.Uint64
	startedAt time.Time
}

type Server struct {
	player    *audio.Player
	bundle    *certs.Bundle
	httpsAddr string
	httpAddr  string

	connMu  sync.Mutex
	current *session

	upgrader websocket.Upgrader
}

func New(player *audio.Player, bundle *certs.Bundle, httpsAddr, httpAddr string) *Server {
	return &Server{
		player:    player,
		bundle:    bundle,
		httpsAddr: httpsAddr,
		httpAddr:  httpAddr,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool { return true },
		},
	}
}

func (s *Server) Run(ctx context.Context) error {
	staticSub, err := fs.Sub(web.StaticFS(), "static")
	if err != nil {
		return fmt.Errorf("embed static: %w", err)
	}

	httpsMux := http.NewServeMux()
	httpsMux.Handle("/", http.FileServer(http.FS(staticSub)))
	httpsMux.HandleFunc("/ws", s.handleWS)
	httpsMux.HandleFunc("/status", s.handleStatus)
	httpsMux.HandleFunc("/ca.crt", s.serveCA)

	httpMux := http.NewServeMux()
	httpMux.HandleFunc("/ca.crt", s.serveCA)
	httpMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(r.Host); err == nil {
			host = h
		}
		_, port, _ := net.SplitHostPort(s.httpsAddr)
		target := fmt.Sprintf("https://%s:%s%s", host, port, r.URL.RequestURI())
		http.Redirect(w, r, target, http.StatusFound)
	})

	cert, err := tlsPair(s.bundle)
	if err != nil {
		return err
	}

	httpsSrv := &http.Server{
		Addr:    s.httpsAddr,
		Handler: httpsMux,
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{cert},
		},
	}
	httpSrv := &http.Server{Addr: s.httpAddr, Handler: httpMux}

	errCh := make(chan error, 2)
	go func() {
		slog.Info("done, HTTPS 已监听", "addr", s.httpsAddr)
		if err := httpsSrv.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
			errCh <- fmt.Errorf("https listen: %w", err)
		}
	}()
	go func() {
		slog.Info("done, HTTP 已监听(仅CA下载/跳转)", "addr", s.httpAddr)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- fmt.Errorf("http listen: %w", err)
		}
	}()
	go s.statsLoop(ctx)

	select {
	case err := <-errCh:
		_ = httpsSrv.Close()
		_ = httpSrv.Close()
		return err
	case <-ctx.Done():
		slog.Info("done, 收到退出信号, 关闭服务")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = httpsSrv.Shutdown(shutdownCtx)
		_ = httpSrv.Shutdown(shutdownCtx)
		s.dropCurrent("server shutdown")
		return nil
	}
}

func (s *Server) serveCA(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/x-x509-ca-cert")
	w.Header().Set("Content-Disposition", `attachment; filename="phoneMic-CA.crt"`)
	_, _ = w.Write(s.bundle.CAPEM)
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	bufferedMs, dropOldest, underrun, written := s.player.JitterBuffer().Stats()
	silentCb, totalCb := s.player.CallbackStats()

	s.connMu.Lock()
	cur := s.current
	s.connMu.Unlock()

	status := map[string]any{
		"device":          s.player.DeviceName(),
		"streaming":       cur != nil,
		"bufferedMs":      bufferedMs,
		"dropOldestBytes": dropOldest,
		"underrunBytes":   underrun,
		"writtenBytes":    written,
		"silentCallbacks": silentCb,
		"totalCallbacks":  totalCb,
	}
	if cur != nil {
		status["sessionBytes"] = cur.bytes.Load()
		status["sessionFrames"] = cur.frames.Load()
		status["remote"] = cur.remote
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(status)
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Error("fail, WS 升级失败", "remote", r.RemoteAddr, "err", err)
		return
	}

	sess := &session{conn: conn, remote: r.RemoteAddr, startedAt: time.Now()}
	s.connMu.Lock()
	old := s.current
	s.current = sess
	s.connMu.Unlock()

	if old != nil {
		slog.Info("hit, 新推流接入, 踢掉旧连接", "old", old.remote, "new", sess.remote)
		_ = old.conn.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(kickCloseCode, "replaced by new sender"),
			time.Now().Add(time.Second))
		_ = old.conn.Close()
	}

	s.player.JitterBuffer().Reset()
	slog.Info("recv, 手机推流已连接", "remote", sess.remote)

	conn.SetReadLimit(wsReadLimit)
	_ = conn.SetReadDeadline(time.Now().Add(wsPongTimeout))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(wsPongTimeout))
	})

	pingDone := make(chan struct{})
	go func() {
		ticker := time.NewTicker(wsPingInterval)
		defer ticker.Stop()
		for {
			select {
			case <-pingDone:
				return
			case <-ticker.C:
				if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(time.Second)); err != nil {
					return
				}
			}
		}
	}()

	var lastOverflowLog time.Time
	for {
		msgType, data, err := conn.ReadMessage()
		if err != nil {
			break
		}
		if msgType != websocket.BinaryMessage || len(data) == 0 {
			continue
		}
		sess.bytes.Add(uint64(len(data)))
		sess.frames.Add(1)
		if dropped := s.player.JitterBuffer().Write(data); dropped > 0 && time.Since(lastOverflowLog) > time.Second {
			lastOverflowLog = time.Now()
			bufferedMs, dropTotal, _, _ := s.player.JitterBuffer().Stats()
			slog.Info("hit, 缓冲超上限丢最旧音频", "判定", "buffered > 300ms", "droppedBytes", dropped, "bufferedMs", bufferedMs, "dropTotalBytes", dropTotal)
		}
	}
	close(pingDone)
	_ = conn.Close()

	s.connMu.Lock()
	if s.current == sess {
		s.current = nil
	}
	s.connMu.Unlock()

	bufferedMs, dropTotal, underrun, _ := s.player.JitterBuffer().Stats()
	slog.Info("done, 手机推流断开", "remote", sess.remote,
		"durationSec", int(time.Since(sess.startedAt).Seconds()),
		"sessionBytes", sess.bytes.Load(), "sessionFrames", sess.frames.Load(),
		"bufferedMs", bufferedMs, "dropTotalBytes", dropTotal, "underrunBytes", underrun)
}

func (s *Server) dropCurrent(reason string) {
	s.connMu.Lock()
	cur := s.current
	s.current = nil
	s.connMu.Unlock()
	if cur != nil {
		_ = cur.conn.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseGoingAway, reason),
			time.Now().Add(time.Second))
		_ = cur.conn.Close()
	}
}

func (s *Server) statsLoop(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.connMu.Lock()
			cur := s.current
			s.connMu.Unlock()
			if cur == nil {
				continue
			}
			bufferedMs, dropTotal, underrun, _ := s.player.JitterBuffer().Stats()
			silentCb, totalCb := s.player.CallbackStats()
			slog.Info("hit, 推流中", "remote", cur.remote, "sessionBytes", cur.bytes.Load(),
				"bufferedMs", bufferedMs, "bufferedTargetMs", audio.TargetBufferMs,
				"dropTotalBytes", dropTotal, "underrunBytes", underrun,
				"silentCallbacks", silentCb, "totalCallbacks", totalCb)
		}
	}
}

func tlsPair(b *certs.Bundle) (tls.Certificate, error) {
	cert, err := tls.X509KeyPair(b.CertPEM, b.KeyPEM)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("load tls keypair: %w", err)
	}
	return cert, nil
}

func BannerURLs(httpsAddr string) []string {
	_, port, _ := net.SplitHostPort(httpsAddr)
	var urls []string
	for _, ip := range certs.LocalIPv4s() {
		urls = append(urls, fmt.Sprintf("https://%s:%s", ip.String(), port))
	}
	return urls
}
