//go:build windows

package main

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

func showStartupError(err error) {
	message := fmt.Sprintf("luneAgent 桌面端无法启动。\n\n如果错误提到 WebView2，请安装 Microsoft Edge WebView2 Runtime。Windows 10/11 通常已预装，下载地址：https://developer.microsoft.com/microsoft-edge/webview2/\n\n错误详情：%v", err)
	text, _ := windows.UTF16PtrFromString(message)
	title, _ := windows.UTF16PtrFromString("luneAgent 启动失败")
	proc := windows.NewLazySystemDLL("user32.dll").NewProc("MessageBoxW")
	_, _, _ = proc.Call(0, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(title)), 0x10)
}
