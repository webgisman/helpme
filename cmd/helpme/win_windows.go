//go:build windows && amd64

// Direct Win32 calls (user32, gdi32, kernel32): GDI screen capture, mouse/keyboard injection
// (SendInput), clipboard and the consent dialog. No cgo, no third-party dependency.
package main

import (
	"encoding/binary"
	"errors"
	"syscall"
	"unicode/utf16"
	"unsafe"
)

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	gdi32    = syscall.NewLazyDLL("gdi32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")

	pGetDC                         = user32.NewProc("GetDC")
	pReleaseDC                     = user32.NewProc("ReleaseDC")
	pGetSystemMetrics              = user32.NewProc("GetSystemMetrics")
	pSetProcessDPIAware            = user32.NewProc("SetProcessDPIAware")
	pSetProcessDpiAwarenessContext = user32.NewProc("SetProcessDpiAwarenessContext")
	pSendInput                     = user32.NewProc("SendInput")
	pGetCursorPos                  = user32.NewProc("GetCursorPos")
	pMessageBoxW                   = user32.NewProc("MessageBoxW")
	pOpenClipboard                 = user32.NewProc("OpenClipboard")
	pCloseClipboard                = user32.NewProc("CloseClipboard")
	pEmptyClipboard                = user32.NewProc("EmptyClipboard")
	pSetClipboardData              = user32.NewProc("SetClipboardData")
	pCreateCompatibleDC            = gdi32.NewProc("CreateCompatibleDC")
	pCreateDIBSection              = gdi32.NewProc("CreateDIBSection")
	pSelectObject                  = gdi32.NewProc("SelectObject")
	pBitBlt                        = gdi32.NewProc("BitBlt")
	pDeleteObject                  = gdi32.NewProc("DeleteObject")
	pDeleteDC                      = gdi32.NewProc("DeleteDC")
	pGlobalAlloc                   = kernel32.NewProc("GlobalAlloc")
	pGlobalLock                    = kernel32.NewProc("GlobalLock")
	pGlobalUnlock                  = kernel32.NewProc("GlobalUnlock")
	pGetUserDefaultUILanguage      = kernel32.NewProc("GetUserDefaultUILanguage")
	pSetConsoleTitleW              = kernel32.NewProc("SetConsoleTitleW")
)

func utf16ptr(s string) *uint16 {
	p, _ := syscall.UTF16PtrFromString(s)
	return p
}

// setDPIAware makes capture and coordinates use physical pixels on scaled displays (125 %, 150 %…).
func setDPIAware() {
	if pSetProcessDpiAwarenessContext.Find() == nil {
		if r, _, _ := pSetProcessDpiAwarenessContext.Call(^uintptr(3)); r != 0 { // DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 (-4)
			return
		}
	}
	pSetProcessDPIAware.Call()
}

func isFrench() bool {
	id, _, _ := pGetUserDefaultUILanguage.Call()
	return id&0x3ff == 0x0c
}

func setTitle(t string) { pSetConsoleTitleW.Call(uintptr(unsafe.Pointer(utf16ptr(t)))) }

func screenSize() (int, int) {
	w, _, _ := pGetSystemMetrics.Call(0) // SM_CXSCREEN: primary monitor
	h, _, _ := pGetSystemMetrics.Call(1)
	return int(w), int(h)
}

func cursorPos() (int, int) {
	var pt [2]int32
	pGetCursorPos.Call(uintptr(unsafe.Pointer(&pt[0])))
	return int(pt[0]), int(pt[1])
}

// askConsent shows a topmost Yes/No dialog; the default button is No.
func askConsent(title, text string) bool {
	const flags = 0x4 | 0x20 | 0x100 | 0x10000 | 0x40000 // YESNO | ICONQUESTION | DEFBUTTON2 | SETFOREGROUND | TOPMOST
	r, _, _ := pMessageBoxW.Call(0, uintptr(unsafe.Pointer(utf16ptr(text))), uintptr(unsafe.Pointer(utf16ptr(title))), flags)
	return r == 6 // IDYES
}

func copyToClipboard(s string) bool {
	if r, _, _ := pOpenClipboard.Call(0); r == 0 {
		return false
	}
	defer pCloseClipboard.Call()
	pEmptyClipboard.Call()
	u := utf16.Encode([]rune(s + "\x00"))
	h, _, _ := pGlobalAlloc.Call(2, uintptr(len(u)*2)) // GMEM_MOVEABLE
	if h == 0 {
		return false
	}
	p, _, _ := pGlobalLock.Call(h) // memory owned by Windows (GlobalAlloc)
	if p == 0 {
		return false
	}
	copy(unsafe.Slice((*uint16)(unsafe.Pointer(p)), len(u)), u)
	pGlobalUnlock.Call(h)
	r, _, _ := pSetClipboardData.Call(13, h) // CF_UNICODETEXT
	return r != 0
}

// ---------------------------------------------------------------- GDI capture

type capturer struct {
	x, y, w, h           int // monitor rectangle in virtual-screen coordinates
	screenDC, memDC, bmp uintptr
	old                  uintptr
	bits                 []byte // BGRA, top-down
}

func newCapturer(m monitor) (*capturer, error) {
	w, h := m.W, m.H
	if w <= 0 || h <= 0 {
		return nil, errors.New("no screen")
	}
	c := &capturer{x: m.X, y: m.Y, w: w, h: h}
	c.screenDC, _, _ = pGetDC.Call(0)
	c.memDC, _, _ = pCreateCompatibleDC.Call(c.screenDC)
	var bmi [44]byte // BITMAPINFOHEADER (40) + 1 RGBQUAD
	binary.LittleEndian.PutUint32(bmi[0:], 40)
	binary.LittleEndian.PutUint32(bmi[4:], uint32(int32(w)))
	binary.LittleEndian.PutUint32(bmi[8:], uint32(int32(-h))) // negative height: top-down DIB
	binary.LittleEndian.PutUint16(bmi[12:], 1)
	binary.LittleEndian.PutUint16(bmi[14:], 32)
	var bits uintptr
	c.bmp, _, _ = pCreateDIBSection.Call(c.memDC, uintptr(unsafe.Pointer(&bmi[0])), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if c.bmp == 0 || bits == 0 {
		c.close()
		return nil, errors.New("CreateDIBSection failed")
	}
	c.old, _, _ = pSelectObject.Call(c.memDC, c.bmp)
	c.bits = unsafe.Slice((*byte)(unsafe.Pointer(bits)), w*h*4)
	return c, nil
}

func (c *capturer) grab() error {
	const srccopy, captureblt = 0x00CC0020, 0x40000000
	r, _, _ := pBitBlt.Call(c.memDC, 0, 0, uintptr(c.w), uintptr(c.h), c.screenDC, uintptr(c.x), uintptr(c.y), srccopy|captureblt)
	if r == 0 {
		return errors.New("capture failed (locked screen?)")
	}
	return nil
}

func (c *capturer) close() {
	if c.old != 0 {
		pSelectObject.Call(c.memDC, c.old)
	}
	if c.bmp != 0 {
		pDeleteObject.Call(c.bmp)
	}
	if c.memDC != 0 {
		pDeleteDC.Call(c.memDC)
	}
	if c.screenDC != 0 {
		pReleaseDC.Call(0, c.screenDC)
	}
	c.bits = nil
}

// ---------------------------------------------------------------- input injection (SendInput)

const inputSize = 40 // sizeof(INPUT) on amd64

func sendInputs(in [][inputSize]byte) {
	if len(in) == 0 {
		return
	}
	buf := make([]byte, 0, len(in)*inputSize)
	for _, b := range in {
		buf = append(buf, b[:]...)
	}
	pSendInput.Call(uintptr(len(in)), uintptr(unsafe.Pointer(&buf[0])), inputSize)
}

func mouseInput(flags uint32, dx, dy int32, data uint32) (b [inputSize]byte) {
	// type 0 (INPUT_MOUSE); MOUSEINPUT at offset 8: dx, dy, mouseData, dwFlags
	binary.LittleEndian.PutUint32(b[8:], uint32(dx))
	binary.LittleEndian.PutUint32(b[12:], uint32(dy))
	binary.LittleEndian.PutUint32(b[16:], data)
	binary.LittleEndian.PutUint32(b[20:], flags)
	return
}

func keyInput(scan uint16, flags uint32) (b [inputSize]byte) {
	binary.LittleEndian.PutUint32(b[0:], 1) // INPUT_KEYBOARD; KEYBDINPUT at offset 8: wVk, wScan, dwFlags
	binary.LittleEndian.PutUint16(b[10:], scan)
	binary.LittleEndian.PutUint32(b[12:], flags)
	return
}

const (
	mouseMove        = 0x0001
	mouseLeftDown    = 0x0002
	mouseLeftUp      = 0x0004
	mouseRightDown   = 0x0008
	mouseRightUp     = 0x0010
	mouseMiddleDown  = 0x0020
	mouseMiddleUp    = 0x0040
	mouseWheel       = 0x0800
	mouseVirtualDesk = 0x4000
	mouseAbsolute    = 0x8000

	keyExtended = 0x0001
	keyUp       = 0x0002
	keyUnicode  = 0x0004
	keyScancode = 0x0008
)

// injectMouse: x, y in pixels of monitor m.
func injectMouse(action, x, y int, m monitor, delta int16) {
	vx, vy, vw, vh := virtualScreen()
	nx := int32(int64(x+m.X-vx) * 65535 / int64(max(1, vw-1)))
	ny := int32(int64(y+m.Y-vy) * 65535 / int64(max(1, vh-1)))
	move := mouseInput(mouseMove|mouseAbsolute|mouseVirtualDesk, nx, ny, 0)
	var extra uint32
	var data uint32
	switch action {
	case 1:
		extra = mouseLeftDown
	case 2:
		extra = mouseLeftUp
	case 3:
		extra = mouseRightDown
	case 4:
		extra = mouseRightUp
	case 5:
		extra = mouseMiddleDown
	case 6:
		extra = mouseMiddleUp
	case 7:
		extra, data = mouseWheel, uint32(int32(delta))
	}
	if extra == 0 {
		sendInputs([][inputSize]byte{move})
		return
	}
	sendInputs([][inputSize]byte{move, mouseInput(extra, 0, 0, data)})
}

func injectKey(down bool, scan uint16, extended bool) {
	flags := uint32(keyScancode)
	if !down {
		flags |= keyUp
	}
	if extended {
		flags |= keyExtended
	}
	sendInputs([][inputSize]byte{keyInput(scan, flags)})
}

func injectText(s string) {
	var in [][inputSize]byte
	for _, u := range utf16.Encode([]rune(s)) {
		in = append(in, keyInput(u, keyUnicode), keyInput(u, keyUnicode|keyUp))
	}
	sendInputs(in)
}
