//go:build windows

package driver

import (
	"encoding/binary"
	"strings"

	"golang.org/x/sys/windows/registry"
)

const (
	renderEndpointsKey  = `SOFTWARE\Microsoft\Windows\CurrentVersion\MMDevices\Audio\Render`
	endpointNameValue   = "{a45c254e-df1c-4efd-8020-67d146a850e0},2"
	endpointFormatValue = "{f19f064d-082c-4e27-bc73-6882a1bb8e4c},0"
	endpointStateValue  = "DeviceState"
	endpointStateActive = 1
)

func CableRenderRate() int {
	root, err := registry.OpenKey(registry.LOCAL_MACHINE, renderEndpointsKey, registry.ENUMERATE_SUB_KEYS)
	if err != nil {
		return 0
	}
	defer root.Close()
	guids, err := root.ReadSubKeyNames(-1)
	if err != nil {
		return 0
	}
	for _, guid := range guids {
		base := renderEndpointsKey + `\` + guid
		dev, err := registry.OpenKey(registry.LOCAL_MACHINE, base, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		state, _, stateErr := dev.GetIntegerValue(endpointStateValue)
		dev.Close()
		if stateErr != nil || state != endpointStateActive {
			continue
		}
		props, err := registry.OpenKey(registry.LOCAL_MACHINE, base+`\Properties`, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		name, _, nameErr := props.GetStringValue(endpointNameValue)
		if nameErr != nil || !strings.Contains(strings.ToLower(name), DefaultMatch) {
			props.Close()
			continue
		}
		blob, _, fmtErr := props.GetBinaryValue(endpointFormatValue)
		props.Close()
		if fmtErr != nil {
			continue
		}
		if rate := parseDeviceFormatRate(blob); rate > 0 {
			return rate
		}
	}
	return 0
}

func parseDeviceFormatRate(blob []byte) int {
	if len(blob) < 26 || binary.LittleEndian.Uint16(blob[8:10]) != 0xFFFE {
		return 0
	}
	return int(binary.LittleEndian.Uint32(blob[12:16]))
}
