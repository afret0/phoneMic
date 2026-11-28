package server

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/afret0/phoneMic/internal/audio"
	"github.com/afret0/phoneMic/internal/certs"
	"github.com/afret0/phoneMic/internal/version"
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
	name      string
	kind      string
	lowLat    bool
	bytes     atomic.Uint64
	frames    atomic.Uint64
	dropped   atomic.Uint64
	startedAt time.Time
}

type Server struct {
	bundle    *certs.Bundle
	httpsAddr string
	httpAddr  string

	ListDevices  func() ([]audio.OutputDevice, error)
	SwitchDevice func(name string) error
	SwitchMode   func(mode string) error
	DeviceAuto   func() bool

	playerMu sync.RWMutex
	player   *audio.Player

	capMu    sync.RWMutex
	capturer *audio.Capturer

	modeMu sync.RWMutex
	mode   string

	connMu       sync.Mutex
	current      *session
	lastSendFail time.Time

	upgrader websocket.Upgrader
}

func New(player *audio.Player, bundle *certs.Bundle, httpsAddr, httpAddr string) *Server {
	return &Server{
		player:    player,
		mode:      "mic",
		bundle:    bundle,
		httpsAddr: httpsAddr,
		httpAddr:  httpAddr,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool { return true },
		},
	}
}

func (s *Server) currentPlayer() *audio.Player {
	s.playerMu.RLock()
	defer s.playerMu.RUnlock()
	return s.player
}

func (s *Server) SetPlayer(p *audio.Player) {
	s.playerMu.Lock()
	old := s.player
	s.player = p
	s.playerMu.Unlock()

	if old == nil && p != nil {
		slog.Info("done, 输出设备已接入", "device", p.DeviceName())
	}
	if old != nil && p == nil {
		slog.Info("hit, 输出设备已摘除", "device", old.DeviceName())
	}
	if old != nil && p != nil {
		slog.Info("hit, 输出设备切换", "old", old.DeviceName(), "new", p.DeviceName())
	}

	s.connMu.Lock()
	cur := s.current
	s.connMu.Unlock()
	if cur != nil && cur.kind == "mic" && p != nil {
		p.JitterBuffer().SetProfile(cur.lowLat)
	}
	if old != nil {
		old.Close()
	}
}

func (s *Server) Mode() string {
	s.modeMu.RLock()
	defer s.modeMu.RUnlock()
	return s.mode
}

func (s *Server) SetMode(m string) {
	s.modeMu.Lock()
	old := s.mode
	s.mode = m
	s.modeMu.Unlock()
	if old != m {
		slog.Info("hit, 工作模式切换", "old", old, "new", m)
	}
}

func (s *Server) currentCapturer() *audio.Capturer {
	s.capMu.RLock()
	defer s.capMu.RUnlock()
	return s.capturer
}

func (s *Server) SetCapturer(c *audio.Capturer) {
	s.capMu.Lock()
	old := s.capturer
	s.capturer = c
	s.capMu.Unlock()

	if old == nil && c != nil {
		slog.Info("done, 采集设备已接入", "device", c.DeviceName())
	}
	if old != nil && c == nil {
		slog.Info("hit, 采集设备已摘除", "device", old.DeviceName())
	}
	if old != nil && c != nil {
		slog.Info("hit, 采集设备切换", "old", old.DeviceName(), "new", c.DeviceName())
	}
	if old != nil {
		old.Close()
	}
}

func (s *Server) CapturerName() string {
	if c := s.currentCapturer(); c != nil {
		return c.DeviceName()
	}
	return ""
}

func (s *Server) ActiveDeviceName() string {
	if name := s.PlayerName(); name != "" {
		return name
	}
	return s.CapturerName()
}

func (s *Server) SendSpeakerPCM(pcm []byte) bool {
	s.connMu.Lock()
	cur := s.current
	s.connMu.Unlock()
	if cur == nil || cur.kind != "speaker" {
		return false
	}
	cur.bytes.Add(uint64(len(pcm)))
	cur.frames.Add(1)
	_ = cur.conn.SetWriteDeadline(time.Now().Add(time.Second))
	if err := cur.conn.WriteMessage(websocket.BinaryMessage, pcm); err != nil {
		s.connMu.Lock()
		logged := time.Since(s.lastSendFail) > time.Second
		if logged {
			s.lastSendFail = time.Now()
		}
		s.connMu.Unlock()
		if logged {
			slog.Error("fail, 下发音频给手机失败, 关闭收听连接", "remote", cur.remote, "err", err)
		}
		_ = cur.conn.Close()
		return false
	}
	return true
}

func (s *Server) KickCurrent(reason string) {
	s.connMu.Lock()
	cur := s.current
	s.current = nil
	s.connMu.Unlock()
	if cur != nil {
		slog.Info("hit, 踢掉当前连接", "remote", cur.remote, "kind", cur.kind, "reason", reason)
		_ = cur.conn.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(kickCloseCode, reason),
			time.Now().Add(time.Second))
		_ = cur.conn.Close()
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
	httpsMux.HandleFunc("/speaker", s.handleSpeakerWS)
	httpsMux.HandleFunc("/status", s.handleStatus)
	httpsMux.HandleFunc("/devices", s.handleDevices)
	httpsMux.HandleFunc("/device", s.handleDeviceSwitch)
	httpsMux.HandleFunc("/mode", s.handleMode)
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

	if mdnsEntry := s.registerMDNS(); mdnsEntry != nil {
		defer mdnsEntry.Shutdown()
	}

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
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(s.Status())
}

func (s *Server) handleDevices(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.ListDevices == nil {
		slog.Error("fail, 设备列表接口未接线")
		http.Error(w, "device list unavailable", http.StatusServiceUnavailable)
		return
	}
	devices, err := s.ListDevices()
	if err != nil {
		slog.Error("fail, 枚举输出设备失败", "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"devices": devices,
		"current": s.ActiveDeviceName(),
		"auto":    s.DeviceAuto != nil && s.DeviceAuto(),
	})
}

func (s *Server) handleMode(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"mode": s.Mode()})
	case http.MethodPost:
		if s.SwitchMode == nil {
			slog.Error("fail, 模式切换接口未接线")
			http.Error(w, "mode switch unavailable", http.StatusServiceUnavailable)
			return
		}
		var req struct {
			Mode string `json:"mode"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); err != nil {
			slog.Error("fail, 模式切换请求解析失败", "remote", r.RemoteAddr, "err", err)
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		if req.Mode != "mic" && req.Mode != "speaker" {
			slog.Info("skip, 模式取值非法", "判定", "mode 只能是 mic 或 speaker", "mode", req.Mode)
			http.Error(w, "invalid mode", http.StatusBadRequest)
			return
		}
		slog.Info("recv, 切换工作模式", "remote", r.RemoteAddr, "mode", req.Mode)
		if err := s.SwitchMode(req.Mode); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"mode": s.Mode()})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleDeviceSwitch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.SwitchDevice == nil {
		slog.Error("fail, 设备切换接口未接线")
		http.Error(w, "device switch unavailable", http.StatusServiceUnavailable)
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); err != nil {
		slog.Error("fail, 设备切换请求解析失败", "remote", r.RemoteAddr, "err", err)
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	slog.Info("recv, 手机端切换声音设备", "remote", r.RemoteAddr, "name", req.Name)
	if err := s.SwitchDevice(req.Name); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"current": s.ActiveDeviceName()})
}

func (s *Server) PlayerName() string {
	if p := s.currentPlayer(); p != nil {
		return p.DeviceName()
	}
	return ""
}

func (s *Server) Status() map[string]any {
	s.connMu.Lock()
	cur := s.current
	s.connMu.Unlock()

	status := map[string]any{
		"streaming": cur != nil,
		"computer":  computerName(),
		"version":   version.Version,
		"mode":      s.Mode(),
	}
	if c := s.currentCapturer(); c != nil {
		captured, noSink, captureCb := c.Stats()
		status["captureDevice"] = c.DeviceName()
		status["capturedBytes"] = captured
		status["noSinkBytes"] = noSink
		status["captureCallbacks"] = captureCb
		status["captureLevel"] = c.Level()
		status["device"] = c.DeviceName()
	}
	if p := s.currentPlayer(); p != nil {
		bufferedMs, dropOldest, underrun, written := p.JitterBuffer().Stats()
		silentCb, totalCb := p.CallbackStats()
		status["device"] = p.DeviceName()
		status["level"] = p.Level()
		status["bufferedMs"] = bufferedMs
		status["bufferedTargetMs"] = p.JitterBuffer().TargetMs()
		status["dropOldestBytes"] = dropOldest
		status["underrunBytes"] = underrun
		status["writtenBytes"] = written
		status["silentCallbacks"] = silentCb
		status["totalCallbacks"] = totalCb
		driftTrims, driftInserts, driftForced := p.JitterBuffer().DriftStats()
		status["driftTrims"] = driftTrims
		status["driftInserts"] = driftInserts
		status["driftForced"] = driftForced
	}
	if cur != nil {
		status["sessionBytes"] = cur.bytes.Load()
		status["sessionFrames"] = cur.frames.Load()
		status["dropSenderBytes"] = cur.dropped.Load()
		status["remote"] = cur.remote
		status["sender"] = cur.name
		status["sessionKind"] = cur.kind
	}
	return status
}

func (s *Server) rejectWrongMode(conn *websocket.Conn, remote, want string) {
	slog.Info("skip, 连接方向与当前模式不符拒绝", "判定", "连接方向须与 mode 一致", "remote", remote, "mode", s.Mode(), "want", want)
	_ = conn.WriteControl(websocket.CloseMessage,
		websocket.FormatCloseMessage(kickCloseCode, "server in "+s.Mode()+" mode"),
		time.Now().Add(time.Second))
	_ = conn.Close()
}

func (s *Server) replaceCurrent(sess *session) {
	s.connMu.Lock()
	old := s.current
	s.current = sess
	s.connMu.Unlock()

	if old != nil {
		slog.Info("hit, 新连接接入, 踢掉旧连接", "old", old.remote, "oldName", old.name, "oldKind", old.kind, "new", sess.remote, "newName", sess.name, "newKind", sess.kind)
		_ = old.conn.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(kickCloseCode, "replaced by new sender"),
			time.Now().Add(time.Second))
		_ = old.conn.Close()
	}
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Error("fail, WS 升级失败", "remote", r.RemoteAddr, "err", err)
		return
	}
	if s.Mode() != "mic" {
		s.rejectWrongMode(conn, r.RemoteAddr, "mic")
		return
	}

	sess := &session{conn: conn, remote: r.RemoteAddr, name: sanitizeSenderName(r.URL.Query().Get("name")), kind: "mic", lowLat: r.URL.Query().Get("lat") == "low", startedAt: time.Now()}
	s.replaceCurrent(sess)

	player := s.currentPlayer()
	if player != nil {
		player.JitterBuffer().SetProfile(sess.lowLat)
	} else {
		slog.Info("hit, 无输出设备, 音频帧将被丢弃", "remote", sess.remote)
	}
	slog.Info("recv, 手机推流已连接", "remote", sess.remote, "name", sess.name, "lowLat", sess.lowLat)

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
		player = s.currentPlayer()
		if player == nil {
			sess.dropped.Add(uint64(len(data)))
			continue
		}
		if dropped := player.JitterBuffer().Write(data); dropped > 0 && time.Since(lastOverflowLog) > time.Second {
			lastOverflowLog = time.Now()
			bufferedMs, dropTotal, _, _ := player.JitterBuffer().Stats()
			slog.Info("hit, 缓冲超上限丢最旧音频", "判定", "buffered > 上限", "maxMs", player.JitterBuffer().MaxMs(), "droppedBytes", dropped, "bufferedMs", bufferedMs, "dropTotalBytes", dropTotal)
		}
	}
	close(pingDone)
	_ = conn.Close()

	s.connMu.Lock()
	if s.current == sess {
		s.current = nil
	}
	s.connMu.Unlock()

	fields := []any{
		"remote", sess.remote,
		"name", sess.name,
		"durationSec", int(time.Since(sess.startedAt).Seconds()),
		"sessionBytes", sess.bytes.Load(), "sessionFrames", sess.frames.Load(),
	}
	if player = s.currentPlayer(); player != nil {
		bufferedMs, dropTotal, underrun, _ := player.JitterBuffer().Stats()
		fields = append(fields, "bufferedMs", bufferedMs, "dropTotalBytes", dropTotal, "underrunBytes", underrun)
	}
	slog.Info("done, 手机推流断开", fields...)
}

func (s *Server) handleSpeakerWS(w http.ResponseWriter, r *http.Request) {
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Error("fail, WS 升级失败", "remote", r.RemoteAddr, "err", err)
		return
	}
	if s.Mode() != "speaker" {
		s.rejectWrongMode(conn, r.RemoteAddr, "speaker")
		return
	}

	sess := &session{conn: conn, remote: r.RemoteAddr, name: sanitizeSenderName(r.URL.Query().Get("name")), kind: "speaker", startedAt: time.Now()}
	s.replaceCurrent(sess)

	if s.currentCapturer() == nil {
		slog.Info("hit, 无采集设备, 暂无音频可下发", "remote", sess.remote)
	}
	slog.Info("recv, 手机收听已连接", "remote", sess.remote, "name", sess.name)

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

	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			break
		}
	}
	close(pingDone)
	_ = conn.Close()

	s.connMu.Lock()
	if s.current == sess {
		s.current = nil
	}
	s.connMu.Unlock()

	fields := []any{
		"remote", sess.remote,
		"name", sess.name,
		"durationSec", int(time.Since(sess.startedAt).Seconds()),
		"sessionBytes", sess.bytes.Load(), "sessionFrames", sess.frames.Load(),
	}
	if c := s.currentCapturer(); c != nil {
		captured, noSink, _ := c.Stats()
		fields = append(fields, "capturedBytes", captured, "noSinkBytes", noSink)
	}
	slog.Info("done, 手机收听断开", fields...)
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
			fields := []any{"remote", cur.remote, "name", cur.name, "kind", cur.kind, "mode", s.Mode(), "sessionBytes", cur.bytes.Load()}
			if c := s.currentCapturer(); c != nil {
				captured, noSink, captureCb := c.Stats()
				fields = append(fields, "capturedBytes", captured, "noSinkBytes", noSink, "captureCallbacks", captureCb)
			}
			if p := s.currentPlayer(); p != nil {
				bufferedMs, dropTotal, underrun, _ := p.JitterBuffer().Stats()
				silentCb, totalCb := p.CallbackStats()
				driftTrims, driftInserts, driftForced := p.JitterBuffer().DriftStats()
				fields = append(fields,
					"bufferedMs", bufferedMs, "bufferedTargetMs", p.JitterBuffer().TargetMs(),
					"dropTotalBytes", dropTotal, "underrunBytes", underrun,
					"silentCallbacks", silentCb, "totalCallbacks", totalCb,
					"driftTrims", driftTrims, "driftInserts", driftInserts, "driftForced", driftForced)
			}
			slog.Info("hit, 推流中", fields...)
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
	for _, ip := range certs.ReachableIPv4s() {
		urls = append(urls, fmt.Sprintf("https://%s:%s", ip.String(), port))
	}
	return urls
}
