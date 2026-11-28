package audio

import (
	"encoding/binary"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/gen2brain/malgo"
)

func ListCaptureDevices() ([]OutputDevice, error) {
	ctx, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		return nil, fmt.Errorf("init audio context: %w", err)
	}
	defer func() {
		_ = ctx.Uninit()
		ctx.Free()
	}()

	infos, err := ctx.Devices(malgo.Capture)
	if err != nil {
		return nil, fmt.Errorf("enumerate capture devices: %w", err)
	}
	devices := make([]OutputDevice, 0, len(infos))
	for _, info := range infos {
		devices = append(devices, OutputDevice{Name: info.Name(), IsDefault: info.IsDefault != 0})
	}
	return devices, nil
}

type Capturer struct {
	ctx        *malgo.AllocatedContext
	dev        *malgo.Device
	deviceName string

	sinkMu sync.Mutex
	sink   func([]byte) bool

	captured  atomic.Uint64
	noSink    atomic.Uint64
	totalCb   atomic.Uint64
	levelBits atomic.Uint64

	lastNoSinkLog time.Time
}

func NewCapturer(match string, sink func([]byte) bool) (*Capturer, error) {
	ctx, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		return nil, fmt.Errorf("init audio context: %w", err)
	}

	infos, err := ctx.Devices(malgo.Capture)
	if err != nil {
		_ = ctx.Uninit()
		ctx.Free()
		return nil, fmt.Errorf("enumerate capture devices: %w", err)
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
		return nil, fmt.Errorf("找不到匹配 %q 的采集设备, 现有采集设备: %s", match, strings.Join(names, " | "))
	}

	c := &Capturer{ctx: ctx, deviceName: picked.Name(), sink: sink}

	cfg := malgo.DefaultDeviceConfig(malgo.Capture)
	cfg.SampleRate = SampleRate
	cfg.PeriodSizeInMilliseconds = 10
	cfg.Capture.Format = malgo.FormatS16
	cfg.Capture.Channels = 1
	cfg.Capture.DeviceID = unsafe.Pointer(&picked.ID)

	callbacks := malgo.DeviceCallbacks{
		Data: func(_, input []byte, frameCount uint32) {
			c.totalCb.Add(1)
			n := int(frameCount) * BytesPerSample
			if n <= 0 || n > len(input) {
				return
			}
			c.captured.Add(uint64(n))
			var sum float64
			for i := 0; i+1 < n; i += 2 {
				v := float64(int16(binary.LittleEndian.Uint16(input[i:i+2]))) / 32768
				sum += v * v
			}
			c.levelBits.Store(math.Float64bits(math.Sqrt(sum / float64(frameCount))))

			c.sinkMu.Lock()
			sink := c.sink
			c.sinkMu.Unlock()
			if sink == nil {
				c.countNoSink(n)
				return
			}
			pcm := make([]byte, n)
			copy(pcm, input[:n])
			if !sink(pcm) {
				c.countNoSink(n)
			}
		},
	}
	dev, err := malgo.InitDevice(ctx.Context, cfg, callbacks)
	if err != nil {
		_ = ctx.Uninit()
		ctx.Free()
		return nil, fmt.Errorf("init capture device %s: %w", picked.Name(), err)
	}
	c.dev = dev

	if err := dev.Start(); err != nil {
		dev.Uninit()
		_ = ctx.Uninit()
		ctx.Free()
		return nil, fmt.Errorf("start capture device %s: %w", picked.Name(), err)
	}

	slog.Info("done, 虚拟声卡采集已启动", "device", picked.Name(), "sampleRate", SampleRate, "channels", 1)
	return c, nil
}

func (c *Capturer) countNoSink(n int) {
	c.noSink.Add(uint64(n))
	if time.Since(c.lastNoSinkLog) > 5*time.Second {
		c.lastNoSinkLog = time.Now()
		slog.Info("skip, 无收听连接, 采集音频丢弃", "device", c.deviceName, "noSinkBytes", c.noSink.Load())
	}
}

func (c *Capturer) DeviceName() string { return c.deviceName }

func (c *Capturer) Stats() (captured, noSink, totalCb uint64) {
	return c.captured.Load(), c.noSink.Load(), c.totalCb.Load()
}

func (c *Capturer) Level() float64 { return math.Float64frombits(c.levelBits.Load()) }

func (c *Capturer) Close() {
	c.sinkMu.Lock()
	c.sink = nil
	c.sinkMu.Unlock()
	_ = c.dev.Stop()
	c.dev.Uninit()
	_ = c.ctx.Uninit()
	c.ctx.Free()
}
