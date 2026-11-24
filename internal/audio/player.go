package audio

import (
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"unsafe"

	"github.com/gen2brain/malgo"
)

type OutputDevice struct {
	Name      string
	IsDefault bool
}

func ListOutputDevices() ([]OutputDevice, error) {
	ctx, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		return nil, fmt.Errorf("init audio context: %w", err)
	}
	defer func() {
		_ = ctx.Uninit()
		ctx.Free()
	}()

	infos, err := ctx.Devices(malgo.Playback)
	if err != nil {
		return nil, fmt.Errorf("enumerate playback devices: %w", err)
	}
	devices := make([]OutputDevice, 0, len(infos))
	for _, info := range infos {
		devices = append(devices, OutputDevice{Name: info.Name(), IsDefault: info.IsDefault != 0})
	}
	return devices, nil
}

type Player struct {
	ctx        *malgo.AllocatedContext
	dev        *malgo.Device
	jb         *JitterBuffer
	deviceName string
	silentCb   atomic.Uint64
	totalCb    atomic.Uint64
}

func NewPlayer(match string) (*Player, error) {
	ctx, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		return nil, fmt.Errorf("init audio context: %w", err)
	}

	infos, err := ctx.Devices(malgo.Playback)
	if err != nil {
		_ = ctx.Uninit()
		ctx.Free()
		return nil, fmt.Errorf("enumerate playback devices: %w", err)
	}

	var picked *malgo.DeviceInfo
	var names []string
	for i := range infos {
		names = append(names, infos[i].Name())
		if strings.Contains(strings.ToLower(infos[i].Name()), strings.ToLower(match)) {
			picked = &infos[i]
			break
		}
	}
	if picked == nil {
		_ = ctx.Uninit()
		ctx.Free()
		return nil, fmt.Errorf("找不到匹配 %q 的输出设备, 现有输出设备: %s", match, strings.Join(names, " | "))
	}

	p := &Player{ctx: ctx, jb: NewJitterBuffer(), deviceName: picked.Name()}

	cfg := malgo.DefaultDeviceConfig(malgo.Playback)
	cfg.SampleRate = SampleRate
	cfg.PeriodSizeInMilliseconds = 10
	cfg.Playback.Format = malgo.FormatS16
	cfg.Playback.Channels = 1
	cfg.Playback.DeviceID = unsafe.Pointer(&picked.ID)

	callbacks := malgo.DeviceCallbacks{
		Data: func(output, _ []byte, frameCount uint32) {
			p.totalCb.Add(1)
			if p.jb.Read(output[:int(frameCount)*BytesPerSample]) {
				p.silentCb.Add(1)
			}
		},
	}
	dev, err := malgo.InitDevice(ctx.Context, cfg, callbacks)
	if err != nil {
		_ = ctx.Uninit()
		ctx.Free()
		return nil, fmt.Errorf("init playback device %s: %w", picked.Name(), err)
	}
	p.dev = dev

	if err := dev.Start(); err != nil {
		dev.Uninit()
		_ = ctx.Uninit()
		ctx.Free()
		return nil, fmt.Errorf("start playback device %s: %w", picked.Name(), err)
	}

	slog.Info("done, 虚拟声卡输出已启动", "device", picked.Name(), "sampleRate", SampleRate, "channels", 1)
	return p, nil
}

func (p *Player) JitterBuffer() *JitterBuffer { return p.jb }

func (p *Player) DeviceName() string { return p.deviceName }

func (p *Player) CallbackStats() (silent, total uint64) {
	return p.silentCb.Load(), p.totalCb.Load()
}

func (p *Player) Close() {
	_ = p.dev.Stop()
	p.dev.Uninit()
	_ = p.ctx.Uninit()
	p.ctx.Free()
}
