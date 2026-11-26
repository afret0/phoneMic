package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/afret0/phoneMic/internal/audio"
)

func Test_HandleDevices(t *testing.T) {
	cases := []struct {
		name     string
		method   string
		wire     bool
		listErr  error
		wantCode int
	}{
		{name: "正常返回设备列表", method: http.MethodGet, wire: true, wantCode: http.StatusOK},
		{name: "非GET拒绝", method: http.MethodPost, wire: true, wantCode: http.StatusMethodNotAllowed},
		{name: "未接线返回503", method: http.MethodGet, wire: false, wantCode: http.StatusServiceUnavailable},
		{name: "枚举失败返回500", method: http.MethodGet, wire: true, listErr: errors.New("boom"), wantCode: http.StatusInternalServerError},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := &Server{}
			if c.wire {
				s.ListDevices = func() ([]audio.OutputDevice, error) {
					if c.listErr != nil {
						return nil, c.listErr
					}
					return []audio.OutputDevice{{Name: "CABLE Input", IsDefault: true}}, nil
				}
				s.DeviceAuto = func() bool { return true }
			}
			w := httptest.NewRecorder()
			s.handleDevices(w, httptest.NewRequest(c.method, "/devices", nil))
			if w.Code != c.wantCode {
				t.Fatalf("code = %d, want %d, body: %s", w.Code, c.wantCode, w.Body.String())
			}
			if c.wantCode != http.StatusOK {
				return
			}
			var got struct {
				Devices []audio.OutputDevice `json:"devices"`
				Current string               `json:"current"`
				Auto    bool                 `json:"auto"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
				t.Fatalf("响应不是合法JSON, err: %s", err)
			}
			if len(got.Devices) != 1 || got.Devices[0].Name != "CABLE Input" || !got.Devices[0].IsDefault {
				t.Fatalf("devices = %+v", got.Devices)
			}
			if got.Current != "" || !got.Auto {
				t.Fatalf("current = %q, auto = %t", got.Current, got.Auto)
			}
		})
	}
}

func Test_HandleDeviceSwitch(t *testing.T) {
	cases := []struct {
		name      string
		method    string
		wire      bool
		body      string
		switchErr error
		wantCode  int
		wantName  string
	}{
		{name: "切换成功", method: http.MethodPost, wire: true, body: `{"name":"CABLE-A Input"}`, wantCode: http.StatusOK, wantName: "CABLE-A Input"},
		{name: "空名字切回自动", method: http.MethodPost, wire: true, body: `{"name":""}`, wantCode: http.StatusOK, wantName: ""},
		{name: "非POST拒绝", method: http.MethodGet, wire: true, body: `{"name":""}`, wantCode: http.StatusMethodNotAllowed},
		{name: "未接线返回503", method: http.MethodPost, wire: false, body: `{"name":""}`, wantCode: http.StatusServiceUnavailable},
		{name: "请求体非法返回400", method: http.MethodPost, wire: true, body: `{bad`, wantCode: http.StatusBadRequest},
		{name: "切换失败返回409", method: http.MethodPost, wire: true, body: `{"name":"不存在"}`, switchErr: errors.New("找不到匹配"), wantCode: http.StatusConflict},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := &Server{}
			var gotName string
			if c.wire {
				s.SwitchDevice = func(name string) error {
					gotName = name
					return c.switchErr
				}
			}
			w := httptest.NewRecorder()
			s.handleDeviceSwitch(w, httptest.NewRequest(c.method, "/device", strings.NewReader(c.body)))
			if w.Code != c.wantCode {
				t.Fatalf("code = %d, want %d, body: %s", w.Code, c.wantCode, w.Body.String())
			}
			if c.wantCode == http.StatusOK {
				if gotName != c.wantName {
					t.Fatalf("切换入参 = %q, want %q", gotName, c.wantName)
				}
				var got struct {
					Current string `json:"current"`
				}
				if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
					t.Fatalf("响应不是合法JSON, err: %s", err)
				}
			}
		})
	}
}