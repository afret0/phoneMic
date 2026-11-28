//go:build darwin

package usb

import _ "embed"

//go:embed adb_assets/adb_darwin
var adbDarwin []byte

var assetFiles = []assetFile{
	{name: "adb", data: adbDarwin, exec: true},
}
