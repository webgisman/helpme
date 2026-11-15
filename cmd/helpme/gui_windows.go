//go:build windows && amd64

// Small native window (plain Win32 controls, no UI toolkit): the short code, the link, the help-request
// button, the session status and a Stop button.
package main

import (
	"encoding/binary"
	"syscall"
	"unsafe"
)

var (
	pRegisterClassExW = user32.NewProc("RegisterClassExW")
	pCreateWindowExW  = user32.NewProc("CreateWindowExW")
	pDefWindowProcW   = user32.NewProc("DefWindowProcW")
	pGetMessageW      = user32.NewProc("GetMessageW")
	pTranslateMessage = user32.NewProc("TranslateMessage")
	pDispatchMessageW = user32.NewProc("DispatchMessageW")
	pPostQuitMessage  = user32.NewProc("PostQuitMessage")
	pSendMessageW     = user32.NewProc("SendMessageW")
	pSetWindowTextW   = user32.NewProc("SetWindowTextW")
	pEnableWindow     = user32.NewProc("EnableWindow")
	pShowWindow       = user32.NewProc("ShowWindow")
	pInvalidateRect   = user32.NewProc("InvalidateRect")
	pLoadCursorW      = user32.NewProc("LoadCursorW")
	pLoadIconW        = user32.NewProc("LoadIconW")
	pGetSysColorBrush = user32.NewProc("GetSysColorBrush")
	pGetSysColor      = user32.NewProc("GetSysColor")
	pGetDpiForSystem  = user32.NewProc("GetDpiForSystem")
	pFlashWindow      = user32.NewProc("FlashWindow")
	pSetForeground    = user32.NewProc("SetForegroundWindow")
	pGetModuleHandleW = kernel32.NewProc("GetModuleHandleW")
	pCreateFontW      = gdi32.NewProc("CreateFontW")
	pSetTextColor     = gdi32.NewProc("SetTextColor")
	pSetBkColor       = gdi32.NewProc("SetBkColor")
)

const (
	wmDestroy         = 0x0002
	wmSetFont         = 0x0030
	wmCommand         = 0x0111
	wmCtlColorStatic  = 0x0138
	wsChild           = 0x40000000
	wsVisible         = 0x10000000
	wsTabStop         = 0x00010000
	wsBorder          = 0x00800000
	wsOverlappedFixed = 0x00CA0000 // caption | sysmenu | minimize box
	ssCenter          = 0x1
	esReadOnly        = 0x0800
	esAutoHScroll     = 0x0080
	bsDefPush         = 0x1

	idCopy = 101 + iota
	idEmail
	idRequest
	idAdmin
	idStop
)

type statusKind int

const (
	stIdle statusKind = iota
	stActive
	stWarn
)

type gui struct {
	hwnd                                        uintptr
	code, link, status, request, admin, stop    uintptr
	statusKind                                  statusKind
	onCopy, onEmail, onRequest, onAdmin, onStop func()
}

var theGUI *gui

func rgb(r, g, b uint32) uintptr { return uintptr(r | g<<8 | b<<16) }

func (g *gui) wndProc(hwnd, msg, wparam, lparam uintptr) uintptr {
	switch msg {
	case wmCommand:
		var f func()
		switch wparam & 0xffff {
		case idCopy:
			f = g.onCopy
		case idEmail:
			f = g.onEmail
		case idRequest:
			f = g.onRequest
		case idAdmin:
			f = g.onAdmin
		case idStop:
			f = g.onStop
		}
		if f != nil {
			go f()
		}
		return 0
	case wmCtlColorStatic:
		bg, _, _ := pGetSysColor.Call(5) // COLOR_WINDOW
		pSetBkColor.Call(wparam, bg)
		switch {
		case lparam == g.code:
			pSetTextColor.Call(wparam, rgb(79, 70, 229))
		case lparam == g.status && g.statusKind == stActive:
			pSetTextColor.Call(wparam, rgb(200, 30, 30))
		case lparam == g.status && g.statusKind == stWarn:
			pSetTextColor.Call(wparam, rgb(180, 100, 0))
		}
		br, _, _ := pGetSysColorBrush.Call(5)
		return br
	case wmDestroy:
		pPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := pDefWindowProcW.Call(hwnd, msg, wparam, lparam)
	return r
}

func font(size int, bold bool, scale float64) uintptr {
	weight := 400
	if bold {
		weight = 700
	}
	f, _, _ := pCreateFontW.Call(uintptr(-int(float64(size)*scale)), 0, 0, 0, uintptr(weight), 0, 0, 0, 1, 0, 0, 5, 0,
		uintptr(unsafe.Pointer(utf16ptr("Segoe UI"))))
	return f
}

// newGUI creates the window. withRequest shows the "Request help" button.
func newGUI(title string, withRequest bool, labels map[string]string) *gui {
	g := &gui{}
	theGUI = g
	hinst, _, _ := pGetModuleHandleW.Call(0)
	dpi, _, _ := pGetDpiForSystem.Call()
	if dpi == 0 {
		dpi = 96
	}
	s := float64(dpi) / 96
	px := func(v int) uintptr { return uintptr(int(float64(v) * s)) }

	className := utf16ptr("HelpMeWindow")
	var wc [80]byte
	binary.LittleEndian.PutUint32(wc[0:], 80)
	binary.LittleEndian.PutUint64(wc[8:], uint64(syscall.NewCallback(g.wndProc)))
	binary.LittleEndian.PutUint64(wc[24:], uint64(hinst))
	icon, _, _ := pLoadIconW.Call(hinst, uintptr(unsafe.Pointer(utf16ptr("APP"))))
	binary.LittleEndian.PutUint64(wc[32:], uint64(icon))
	cursor, _, _ := pLoadCursorW.Call(0, 32512)
	binary.LittleEndian.PutUint64(wc[40:], uint64(cursor))
	binary.LittleEndian.PutUint64(wc[48:], 6) // COLOR_WINDOW + 1
	binary.LittleEndian.PutUint64(wc[64:], uint64(uintptr(unsafe.Pointer(className))))
	binary.LittleEndian.PutUint64(wc[72:], uint64(icon))
	pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc[0])))

	const W, H = 460, 470
	sw, sh := screenSize()
	g.hwnd, _, _ = pCreateWindowExW.Call(0x8 /* topmost */, uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(utf16ptr(title))),
		wsOverlappedFixed|wsVisible, uintptr(sw)-px(W)-px(40), uintptr(sh)-px(H)-px(80), px(W+16), px(H+40), 0, 0, hinst, 0)

	fTitle, fText, fCode, fBold, fSmall := font(22, true, s), font(15, false, s), font(38, true, s), font(15, true, s), font(12, false, s)
	add := func(class, text string, style uintptr, x, y, w, h int, id uintptr, f uintptr) uintptr {
		c, _, _ := pCreateWindowExW.Call(0, uintptr(unsafe.Pointer(utf16ptr(class))), uintptr(unsafe.Pointer(utf16ptr(text))),
			wsChild|wsVisible|style, px(x), px(y), px(w), px(h), g.hwnd, id, hinst, 0)
		pSendMessageW.Call(c, wmSetFont, f, 1)
		return c
	}
	add("STATIC", "HelpMe", 0, 20, 14, 420, 34, 0, fTitle)
	add("STATIC", labels["give_code"], 0, 20, 52, 420, 24, 0, fText)
	g.code = add("STATIC", "…", ssCenter, 20, 80, 420, 56, 0, fCode)
	add("STATIC", labels["or_link"], 0, 20, 146, 420, 22, 0, fText)
	g.link = add("EDIT", "", wsBorder|esReadOnly|esAutoHScroll|wsTabStop, 20, 170, 420, 28, 0, fSmall)
	add("BUTTON", labels["copy"], wsTabStop, 20, 208, 205, 34, idCopy, fText)
	add("BUTTON", labels["email"], wsTabStop, 235, 208, 205, 34, idEmail, fText)
	y := 254
	if withRequest {
		g.request = add("BUTTON", labels["request"], wsTabStop|bsDefPush, 20, y, 420, 44, idRequest, fBold)
		y += 54
	}
	g.status = add("STATIC", labels["waiting"], 0, 20, y, 420, 66, 0, fBold)
	y += 74
	g.admin = add("BUTTON", labels["admin"], wsTabStop, 20, y, 205, 34, idAdmin, fText)
	g.stop = add("BUTTON", labels["stop"], wsTabStop, 235, y, 205, 34, idStop, fText)
	pEnableWindow.Call(g.stop, 0)
	add("STATIC", labels["footer"], 0, 20, y+44, 420, 20, 0, fSmall)
	return g
}

func (g *gui) setText(ctl uintptr, s string) {
	pSetWindowTextW.Call(ctl, uintptr(unsafe.Pointer(utf16ptr(s))))
}

func (g *gui) setStatus(kind statusKind, s string) {
	g.statusKind = kind
	g.setText(g.status, s)
	pInvalidateRect.Call(g.status, 0, 1)
}

func (g *gui) setActive(active bool) {
	v := uintptr(0)
	if active {
		v = 1
		pFlashWindow.Call(g.hwnd, 1)
	}
	pEnableWindow.Call(g.stop, v)
}

func (g *gui) hideAdmin() { pShowWindow.Call(g.admin, 0) }

func (g *gui) enableRequest(on bool) {
	if g.request != 0 {
		v := uintptr(0)
		if on {
			v = 1
		}
		pEnableWindow.Call(g.request, v)
	}
}

// run pumps window messages until the window is closed.
func (g *gui) run() {
	var msg [48]byte
	for {
		r, _, _ := pGetMessageW.Call(uintptr(unsafe.Pointer(&msg[0])), 0, 0, 0)
		if int32(r) <= 0 {
			return
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&msg[0])))
		pDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg[0])))
	}
}
