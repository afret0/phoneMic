package usb

import (
	"reflect"
	"testing"
)

func TestParseDevices(t *testing.T) {
	cases := []struct {
		name string
		out  string
		want []Device
	}{
		{"empty", "List of devices attached\n\n", nil},
		{"one device", "List of devices attached\n3ea19dcd\tdevice\n\n", []Device{{Serial: "3ea19dcd", State: "device"}}},
		{"unauthorized", "List of devices attached\nABC123 unauthorized\n", []Device{{Serial: "ABC123", State: "unauthorized"}}},
		{"daemon noise", "* daemon not running; starting now at tcp:5037\n* daemon started successfully\nList of devices attached\nemu-1 device\nxyz offline\n", []Device{{Serial: "emu-1", State: "device"}, {Serial: "xyz", State: "offline"}}},
		{"wireless serial", "adb-3ea19dcd-SxPDP4._adb-tls-connect._tcp\tdevice\n", []Device{{Serial: "adb-3ea19dcd-SxPDP4._adb-tls-connect._tcp", State: "device"}}},
		{"short line", "List of devices attached\nlonely\n", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseDevices(c.out)
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("parseDevices(%q) = %+v, want %+v", c.out, got, c.want)
			}
		})
	}
}

func TestMissingPorts(t *testing.T) {
	cases := []struct {
		name    string
		listOut string
		ports   []int
		want    []int
	}{
		{"empty list", "", []int{8443, 8080}, []int{8443, 8080}},
		{"both present", "3ea19dcd tcp:8443 tcp:8443\n3ea19dcd tcp:8080 tcp:8080\n", []int{8443, 8080}, nil},
		{"one present", "3ea19dcd tcp:8443 tcp:8443\n", []int{8443, 8080}, []int{8080}},
		{"wrong mapping", "3ea19dcd tcp:8443 tcp:9999\n", []int{8443}, []int{8443}},
		{"prefix not exact", "3ea19dcd tcp:84430 tcp:84430\n", []int{8443}, []int{8443}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := missingPorts(c.listOut, c.ports)
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("missingPorts(%q, %v) = %v, want %v", c.listOut, c.ports, got, c.want)
			}
		})
	}
}
