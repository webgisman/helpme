//go:build windows && amd64

package main

import (
	"encoding/binary"
	"os"
	"sort"
	"syscall"
	"unicode/utf16"
	"unsafe"
)

var (
	shell32 = syscall.NewLazyDLL("shell32.dll")
	advapi  = syscall.NewLazyDLL("advapi32.dll")

	pEnumDisplayMonitors        = user32.NewProc("EnumDisplayMonitors")
	pGetMonitorInfoW            = user32.NewProc("GetMonitorInfoW")
	pGetClipboardSequenceNumber = user32.NewProc("GetClipboardSequenceNumber")
	pGetClipboardData           = user32.NewProc("GetClipboardData")
	pIsClipboardFormatAvailable = user32.NewProc("IsClipboardFormatAvailable")
	pGlobalSize                 = kernel32.NewProc("GlobalSize")
	pShellExecuteW              = shell32.NewProc("ShellExecuteW")
	pIsUserAnAdmin              = shell32.NewProc("IsUserAnAdmin")
	pGetComputerNameW           = kernel32.NewProc("GetComputerNameW")
	pGetUserNameW               = advapi.NewProc("GetUserNameW")
)

type monitor struct {
	X, Y, W, H int
	Primary    bool
	All        bool `json:",omitempty"` // the whole virtual desktop (every display side by side)
}

// monitors lists the displays, primary first.
func monitors() []monitor {
	var list []monitor
	cb := syscall.NewCallback(func(hmon, hdc, rc, lparam uintptr) uintptr {
		var mi [40]byte // MONITORINFO: cbSize, rcMonitor, rcWork, dwFlags
		binary.LittleEndian.PutUint32(mi[0:], 40)
		if r, _, _ := pGetMonitorInfoW.Call(hmon, uintptr(unsafe.Pointer(&mi[0]))); r != 0 {
			l := int(int32(binary.LittleEndian.Uint32(mi[4:])))
			t := int(int32(binary.LittleEndian.Uint32(mi[8:])))
			r := int(int32(binary.LittleEndian.Uint32(mi[12:])))
			b := int(int32(binary.LittleEndian.Uint32(mi[16:])))
			list = append(list, monitor{X: l, Y: t, W: r - l, H: b - t, Primary: binary.LittleEndian.Uint32(mi[36:])&1 != 0})
		}
		return 1
	})
	pEnumDisplayMonitors.Call(0, 0, cb, 0)
	sort.SliceStable(list, func(i, j int) bool { return list[i].Primary && !list[j].Primary })
	if len(list) == 0 {
		w, h := screenSize()
		list = []monitor{{W: w, H: h, Primary: true}}
	}
	if len(list) > 1 {
		x, y, w, h := virtualScreen()
		list = append(list, monitor{X: x, Y: y, W: w, H: h, All: true})
	}
	return list
}

func virtualScreen() (x, y, w, h int) {
	get := func(i uintptr) int { v, _, _ := pGetSystemMetrics.Call(i); return int(int32(v)) }
	return get(76), get(77), get(78), get(79) // SM_X/YVIRTUALSCREEN, SM_CX/CYVIRTUALSCREEN
}

func clipboardSeq() uint32 {
	r, _, _ := pGetClipboardSequenceNumber.Call()
	return uint32(r)
}

// readClipboardText returns the clipboard text (CF_UNICODETEXT), "" if none.
func readClipboardText(limit int) string {
	if r, _, _ := pIsClipboardFormatAvailable.Call(13); r == 0 {
		return ""
	}
	if r, _, _ := pOpenClipboard.Call(0); r == 0 {
		return ""
	}
	defer pCloseClipboard.Call()
	h, _, _ := pGetClipboardData.Call(13)
	if h == 0 {
		return ""
	}
	p, _, _ := pGlobalLock.Call(h) // memory owned by the clipboard
	if p == 0 {
		return ""
	}
	defer pGlobalUnlock.Call(h)
	size, _, _ := pGlobalSize.Call(h)
	n := min(int(size)/2, limit)
	u := unsafe.Slice((*uint16)(unsafe.Pointer(p)), n)
	for i, c := range u {
		if c == 0 {
			u = u[:i]
			break
		}
	}
	return string(utf16.Decode(u))
}

func isAdmin() bool {
	r, _, _ := pIsUserAnAdmin.Call()
	return r != 0
}

// shellExecute runs ShellExecuteW; verb "runas" asks for elevation. Returns false if refused.
func shellExecute(verb, file, args string) bool {
	var a uintptr
	if args != "" {
		a = uintptr(unsafe.Pointer(utf16ptr(args)))
	}
	r, _, _ := pShellExecuteW.Call(0, uintptr(unsafe.Pointer(utf16ptr(verb))), uintptr(unsafe.Pointer(utf16ptr(file))), a, 0, 1)
	return r > 32
}

func computerName() string {
	buf := make([]uint16, 64)
	n := uint32(len(buf))
	pGetComputerNameW.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&n)))
	return syscall.UTF16ToString(buf)
}

func userName() string {
	buf := make([]uint16, 128)
	n := uint32(len(buf))
	pGetUserNameW.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&n)))
	if s := syscall.UTF16ToString(buf); s != "" {
		return s
	}
	return os.Getenv("USERNAME")
}
