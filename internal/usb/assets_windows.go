//go:build windows

package usb

import _ "embed"

//go:embed adb_assets/adb.exe
var adbExe []byte

//go:embed adb_assets/AdbWinApi.dll
var adbWinApi []byte

//go:embed adb_assets/AdbWinUsbApi.dll
var adbWinUsbApi []byte

var assetFiles = []assetFile{
	{name: "adb.exe", data: adbExe, exec: true},
	{name: "AdbWinApi.dll", data: adbWinApi},
	{name: "AdbWinUsbApi.dll", data: adbWinUsbApi},
}
