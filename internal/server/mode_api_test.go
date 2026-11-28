package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func Test_HandleMode(t *testing.T) {
	cases := []struct {
		name      string
		method    string
		wire      bool
		body      string
		switchErr error
		wantCode  int
		wantMode  string
	}{
		{name: "GET返回当前模式", method: http.MethodGet, wire: true, wantCode: http.StatusOK, wantMode: "mic"},
		{name: "切喇叭成功", method: http.MethodPost, wire: true, body: `{"mode":"speaker"}`, wantCode: http.StatusOK, wantMode: "speaker"},
		{name: "切麦克风成功", method: http.MethodPost, wire: true, body: `{"mode":"mic"}`, wantCode: http.StatusOK, wantMode: "mic"},
		{name: "非法模式400", method: http.MethodPost, wire: true, body: `{"mode":"duplex"}`, wantCode: http.StatusBadRequest},
		{name: "空模式400", method: http.MethodPost, wire: true, body: `{"mode":""}`, wantCode: http.StatusBadRequest},
		{name: "请求体非法400", method: http.MethodPost, wire: true, body: `{bad`, wantCode: http.StatusBadRequest},
		{name: "未接线503", method: http.MethodPost, wire: false, body: `{"mode":"speaker"}`, wantCode: http.StatusServiceUnavailable},
		{name: "切换失败409", method: http.MethodPost, wire: true, body: `{"mode":"speaker"}`, switchErr: errors.New("找不到采集设备"), wantCode: http.StatusConflict},
		{name: "PUT拒绝405", method: http.MethodPut, wire: true, body: `{"mode":"speaker"}`, wantCode: http.StatusMethodNotAllowed},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := &Server{mode: "mic"}
			var gotMode string
			if c.wire {
				s.SwitchMode = func(mode string) error {
					gotMode = mode
					if c.switchErr == nil {
						s.SetMode(mode)
					}
					return c.switchErr
				}
			}
			w := httptest.NewRecorder()
			s.handleMode(w, httptest.NewRequest(c.method, "/mode", strings.NewReader(c.body)))
			if w.Code != c.wantCode {
				t.Fatalf("code = %d, want %d, body: %s", w.Code, c.wantCode, w.Body.String())
			}
			if c.wantCode != http.StatusOK {
				return
			}
			var got struct {
				Mode string `json:"mode"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
				t.Fatalf("响应不是合法JSON, err: %s", err)
			}
			if got.Mode != c.wantMode {
				t.Fatalf("mode = %q, want %q", got.Mode, c.wantMode)
			}
			if c.method == http.MethodPost && gotMode != strings.TrimSuffix(strings.TrimPrefix(c.body, `{"mode":"`), `"}`) {
				t.Fatalf("切换入参 = %q, body: %s", gotMode, c.body)
			}
		})
	}
}

func waitStreaming(t *testing.T, s *Server, want bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if s.Status()["streaming"] == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("streaming 未在超时内变为 %t, status: %+v", want, s.Status())
}

func dialWS(t *testing.T, ts *httptest.Server, path string) *websocket.Conn {
	t.Helper()
	url := "ws" + strings.TrimPrefix(ts.URL, "http") + path
	conn, resp, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		code := 0
		if resp != nil {
			code = resp.StatusCode
		}
		t.Fatalf("dial %s 失败, httpCode: %d, err: %s", path, code, err)
	}
	return conn
}

func readCloseCode(t *testing.T, conn *websocket.Conn) int {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		_, _, err := conn.ReadMessage()
		if err == nil {
			continue
		}
		if ce, ok := err.(*websocket.CloseError); ok {
			return ce.Code
		}
		t.Fatalf("期待 close 帧, got err: %s", err)
		return 0
	}
}

func Test_SpeakerSessionLifecycle(t *testing.T) {
	s := &Server{mode: "mic"}
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", s.handleWS)
	mux.HandleFunc("/speaker", s.handleSpeakerWS)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	micConn := dialWS(t, ts, "/ws?name=A")
	waitStreaming(t, s, true)

	spkRejected := dialWS(t, ts, "/speaker?name=B")
	if code := readCloseCode(t, spkRejected); code != kickCloseCode {
		t.Fatalf("麦克风模式下 /speaker 应被拒 4001, got %d", code)
	}
	if s.Status()["sessionKind"] != "mic" {
		t.Fatalf("被拒连接不应抢占会话, status: %+v", s.Status())
	}

	s.SetMode("speaker")
	s.KickCurrent("mode switched to speaker")
	if code := readCloseCode(t, micConn); code != kickCloseCode {
		t.Fatalf("模式切换应踢旧连接 4001, got %d", code)
	}
	waitStreaming(t, s, false)

	wsRejected := dialWS(t, ts, "/ws?name=A")
	if code := readCloseCode(t, wsRejected); code != kickCloseCode {
		t.Fatalf("喇叭模式下 /ws 应被拒 4001, got %d", code)
	}

	spk1 := dialWS(t, ts, "/speaker?name=B")
	waitStreaming(t, s, true)
	spk2 := dialWS(t, ts, "/speaker?name=C")
	if code := readCloseCode(t, spk1); code != kickCloseCode {
		t.Fatalf("新收听连接应踢旧收听连接 4001, got %d", code)
	}
	waitStreaming(t, s, true)

	pcm := []byte{1, 0, 2, 0, 3, 0, 4, 0}
	deadline := time.Now().Add(2 * time.Second)
	for s.Status()["sender"] != "C" && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !s.SendSpeakerPCM(pcm) {
		t.Fatalf("有收听连接时下发应成功, status: %+v", s.Status())
	}
	_ = spk2.SetReadDeadline(time.Now().Add(2 * time.Second))
	mt, data, err := spk2.ReadMessage()
	if err != nil {
		t.Fatalf("手机端应收到二进制音频, err: %s", err)
	}
	if mt != websocket.BinaryMessage || string(data) != string(pcm) {
		t.Fatalf("收帧不符, type: %d, data: %+v", mt, data)
	}
	if s.Status()["sessionKind"] != "speaker" {
		t.Fatalf("sessionKind 应为 speaker, status: %+v", s.Status())
	}

	_ = spk2.Close()
	waitStreaming(t, s, false)
	if s.SendSpeakerPCM(pcm) {
		t.Fatal("无收听连接时下发应返回 false")
	}
}
