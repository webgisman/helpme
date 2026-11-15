//go:build windows && amd64

// H.264 encoding with the encoder that ships with Windows (Media Foundation "H264 Encoder MFT"),
// called through COM vtables: no codec bundled, nothing to install. Used for fast-moving content.
package main

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"unsafe"
)

type guid struct {
	d1     uint32
	d2, d3 uint16
	d4     [8]byte
}

func mkguid(s string) *guid {
	p := strings.Split(s, "-")
	a, _ := strconv.ParseUint(p[0], 16, 32)
	b, _ := strconv.ParseUint(p[1], 16, 16)
	c, _ := strconv.ParseUint(p[2], 16, 16)
	g := &guid{d1: uint32(a), d2: uint16(b), d3: uint16(c)}
	rest := p[3] + p[4]
	for i := 0; i < 8; i++ {
		v, _ := strconv.ParseUint(rest[i*2:i*2+2], 16, 8)
		g.d4[i] = byte(v)
	}
	return g
}

var (
	ole32  = syscall.NewLazyDLL("ole32.dll")
	mfplat = syscall.NewLazyDLL("mfplat.dll")

	pCoInitializeEx       = ole32.NewProc("CoInitializeEx")
	pCoCreateInstance     = ole32.NewProc("CoCreateInstance")
	pMFStartup            = mfplat.NewProc("MFStartup")
	pMFCreateMediaType    = mfplat.NewProc("MFCreateMediaType")
	pMFCreateSample       = mfplat.NewProc("MFCreateSample")
	pMFCreateMemoryBuffer = mfplat.NewProc("MFCreateMemoryBuffer")

	clsidH264Encoder = mkguid("6ca50344-051a-4ded-9779-a43305165e35")
	iidIMFTransform  = mkguid("bf94c121-5b05-4e6f-8000-ba598961414d")
	mtMajorType      = mkguid("48eba18e-f8c9-4687-bf11-0a74c9f96a8f")
	mtSubtype        = mkguid("f7e34c9a-42e8-4714-b74b-cb29d72c35e5")
	mtAvgBitrate     = mkguid("20332624-fb0d-4d9e-bd0d-cbf6786c102e")
	mtFrameSize      = mkguid("1652c33d-d6b2-4012-b834-72030849a37d")
	mtFrameRate      = mkguid("c459a2e8-3d2c-4e44-b132-fee5156c7bb0")
	mtPixelAspect    = mkguid("c6376a1e-8d0a-4027-be45-6d9a0ad39bb6")
	mtInterlaceMode  = mkguid("e2724bb8-e676-4806-b4b2-a8d6efb44ccd")
	mtMpeg2Profile   = mkguid("ad76a80b-2d5c-4e0b-b375-64e520137036")
	mfLowLatency     = mkguid("9c27891a-ed7a-40e1-88e8-b22727a024ee")
	mediaTypeVideo   = mkguid("73646976-0000-0010-8000-00aa00389b71")
	videoFormatH264  = mkguid("34363248-0000-0010-8000-00aa00389b71")
	videoFormatNV12  = mkguid("3231564e-0000-0010-8000-00aa00389b71")
	mfStartupOnce    sync.Once
	mfStartupErr     error
)

const (
	hrNeedMoreInput = 0xC00D6D72
	hrStreamChange  = 0xC00D6D61

	// IMFTransform
	vtGetOutputStreamInfo    = 7
	vtGetAttributes          = 8
	vtGetOutputAvailableType = 14
	vtSetInputType           = 15
	vtSetOutputType          = 16
	vtProcessMessage         = 23
	vtProcessInput           = 24
	vtProcessOutput          = 25
	// IMFAttributes
	vtSetUINT32 = 21
	vtSetUINT64 = 22
	vtSetGUID   = 24
	// IMFSample
	vtSetSampleTime             = 36
	vtSetSampleDuration         = 38
	vtConvertToContiguousBuffer = 41
	vtAddBuffer                 = 42
	// IMFMediaBuffer
	vtLock             = 3
	vtUnlock           = 4
	vtSetCurrentLength = 6
)

func com(obj uintptr, idx int, args ...uintptr) uint32 {
	vtbl := *(*uintptr)(unsafe.Pointer(obj))
	fn := *(*uintptr)(unsafe.Pointer(vtbl + uintptr(idx)*unsafe.Sizeof(uintptr(0))))
	r, _, _ := syscall.SyscallN(fn, append([]uintptr{obj}, args...)...)
	return uint32(r)
}

func release(obj uintptr) {
	if obj != 0 {
		com(obj, 2)
	}
}

func failed(hr uint32) bool { return int32(hr) < 0 }

func ptr(g *guid) uintptr { return uintptr(unsafe.Pointer(g)) }

type h264Encoder struct {
	mft      uintptr
	w, h     int
	fps      int
	frame    int64
	outSize  uint32
	provides bool
	nv12     []byte
}

// initCOM must run on the (locked) OS thread that uses the encoder.
func initCOM() error {
	pCoInitializeEx.Call(0, 0) // COINIT_MULTITHREADED ; S_FALSE if already initialised
	mfStartupOnce.Do(func() {
		if err := mfplat.Load(); err != nil {
			mfStartupErr = err
			return
		}
		if hr, _, _ := pMFStartup.Call(0x20070, 0); failed(uint32(hr)) {
			mfStartupErr = fmt.Errorf("MFStartup 0x%x", uint32(hr))
		}
	})
	return mfStartupErr
}

func newMediaType(sub *guid, w, h, fps int) (uintptr, error) {
	var t uintptr
	if hr, _, _ := pMFCreateMediaType.Call(uintptr(unsafe.Pointer(&t))); failed(uint32(hr)) {
		return 0, fmt.Errorf("MFCreateMediaType 0x%x", uint32(hr))
	}
	com(t, vtSetGUID, ptr(mtMajorType), ptr(mediaTypeVideo))
	com(t, vtSetGUID, ptr(mtSubtype), ptr(sub))
	com(t, vtSetUINT64, ptr(mtFrameSize), uintptr(uint64(w)<<32|uint64(h)))
	com(t, vtSetUINT64, ptr(mtFrameRate), uintptr(uint64(fps)<<32|1))
	com(t, vtSetUINT64, ptr(mtPixelAspect), uintptr(uint64(1)<<32|1))
	com(t, vtSetUINT32, ptr(mtInterlaceMode), 2) // progressive
	return t, nil
}

func newH264Encoder(w, h, fps, bitrate int) (*h264Encoder, error) {
	if err := initCOM(); err != nil {
		return nil, err
	}
	w, h = w&^1, h&^1
	e := &h264Encoder{w: w, h: h, fps: fps, nv12: make([]byte, w*h*3/2)}
	if hr, _, _ := pCoCreateInstance.Call(ptr(clsidH264Encoder), 0, 1, ptr(iidIMFTransform), uintptr(unsafe.Pointer(&e.mft))); failed(uint32(hr)) {
		return nil, fmt.Errorf("H.264 encoder unavailable (0x%x)", uint32(hr))
	}
	var attrs uintptr
	if !failed(com(e.mft, vtGetAttributes, uintptr(unsafe.Pointer(&attrs)))) && attrs != 0 {
		com(attrs, vtSetUINT32, ptr(mfLowLatency), 1)
		release(attrs)
	}
	out, err := newMediaType(videoFormatH264, w, h, fps)
	if err != nil {
		e.close()
		return nil, err
	}
	com(out, vtSetUINT32, ptr(mtAvgBitrate), uintptr(bitrate))
	com(out, vtSetUINT32, ptr(mtMpeg2Profile), 66) // Baseline: no B-frames, lowest latency
	hr := com(e.mft, vtSetOutputType, 0, out, 0)
	release(out)
	if failed(hr) {
		e.close()
		return nil, fmt.Errorf("SetOutputType 0x%x", hr)
	}
	in, err := newMediaType(videoFormatNV12, w, h, fps)
	if err != nil {
		e.close()
		return nil, err
	}
	hr = com(e.mft, vtSetInputType, 0, in, 0)
	release(in)
	if failed(hr) {
		e.close()
		return nil, fmt.Errorf("SetInputType 0x%x", hr)
	}
	var info [3]uint32 // dwFlags, cbSize, cbAlignment
	com(e.mft, vtGetOutputStreamInfo, 0, uintptr(unsafe.Pointer(&info[0])))
	e.provides = info[0]&0x100 != 0 // MFT_OUTPUT_STREAM_PROVIDES_SAMPLES
	e.outSize = max(info[1], uint32(w*h))
	com(e.mft, vtProcessMessage, 0x10000000, 0) // NOTIFY_BEGIN_STREAMING
	com(e.mft, vtProcessMessage, 0x10000003, 0) // NOTIFY_START_OF_STREAM
	return e, nil
}

func newBufferSample(size uint32) (sample, buf uintptr, err error) {
	if hr, _, _ := pMFCreateMemoryBuffer.Call(uintptr(size), uintptr(unsafe.Pointer(&buf))); failed(uint32(hr)) {
		return 0, 0, fmt.Errorf("MFCreateMemoryBuffer 0x%x", uint32(hr))
	}
	if hr, _, _ := pMFCreateSample.Call(uintptr(unsafe.Pointer(&sample))); failed(uint32(hr)) {
		release(buf)
		return 0, 0, fmt.Errorf("MFCreateSample 0x%x", uint32(hr))
	}
	com(sample, vtAddBuffer, buf)
	return sample, buf, nil
}

// encode converts a BGRA frame (stride = srcW*4) and returns the H.264 Annex B output produced so far.
func (e *h264Encoder) encode(bgra []byte, srcW int) ([]byte, error) {
	if srcW == e.w {
		bgraToNV12(bgra, e.w, e.h, e.nv12)
	} else { // odd width: drop the last column
		tmp := make([]byte, e.w*e.h*4)
		for y := 0; y < e.h; y++ {
			copy(tmp[y*e.w*4:(y+1)*e.w*4], bgra[y*srcW*4:])
		}
		bgraToNV12(tmp, e.w, e.h, e.nv12)
	}
	sample, buf, err := newBufferSample(uint32(len(e.nv12)))
	if err != nil {
		return nil, err
	}
	var p uintptr
	com(buf, vtLock, uintptr(unsafe.Pointer(&p)), 0, 0)
	copy(unsafe.Slice((*byte)(unsafe.Pointer(p)), len(e.nv12)), e.nv12) // MF-owned memory
	com(buf, vtUnlock)
	com(buf, vtSetCurrentLength, uintptr(len(e.nv12)))
	dur := int64(10_000_000 / e.fps)
	com(sample, vtSetSampleTime, uintptr(e.frame*dur))
	com(sample, vtSetSampleDuration, uintptr(dur))
	e.frame++
	hr := com(e.mft, vtProcessInput, 0, sample, 0)
	release(buf)
	release(sample)
	if failed(hr) {
		return nil, fmt.Errorf("ProcessInput 0x%x", hr)
	}
	var out []byte
	for i := 0; i < 8; i++ {
		chunk, more, err := e.drain()
		if err != nil {
			return out, err
		}
		out = append(out, chunk...)
		if !more {
			break
		}
	}
	return out, nil
}

func (e *h264Encoder) drain() ([]byte, bool, error) {
	var own, ownBuf uintptr
	if !e.provides {
		var err error
		if own, ownBuf, err = newBufferSample(e.outSize); err != nil {
			return nil, false, err
		}
		defer release(ownBuf)
		defer release(own)
	}
	var odb [4]uintptr // MFT_OUTPUT_DATA_BUFFER: dwStreamID, pSample, dwStatus, pEvents
	odb[1] = own
	var status uint32
	hr := com(e.mft, vtProcessOutput, 0, 1, uintptr(unsafe.Pointer(&odb[0])), uintptr(unsafe.Pointer(&status)))
	if odb[3] != 0 {
		release(odb[3])
	}
	switch {
	case hr == hrNeedMoreInput:
		return nil, false, nil
	case hr == hrStreamChange:
		var t uintptr
		if !failed(com(e.mft, vtGetOutputAvailableType, 0, 0, uintptr(unsafe.Pointer(&t)))) {
			com(e.mft, vtSetOutputType, 0, t, 0)
			release(t)
		}
		return nil, true, nil
	case failed(hr):
		return nil, false, fmt.Errorf("ProcessOutput 0x%x", hr)
	}
	s := odb[1]
	if s == 0 {
		return nil, false, errors.New("no output sample")
	}
	if e.provides {
		defer release(s)
	}
	var mb uintptr
	if failed(com(s, vtConvertToContiguousBuffer, uintptr(unsafe.Pointer(&mb)))) {
		return nil, false, errors.New("ConvertToContiguousBuffer failed")
	}
	defer release(mb)
	var p uintptr
	var n uint32
	com(mb, vtLock, uintptr(unsafe.Pointer(&p)), 0, uintptr(unsafe.Pointer(&n)))
	data := append([]byte(nil), unsafe.Slice((*byte)(unsafe.Pointer(p)), n)...)
	com(mb, vtUnlock)
	return data, true, nil
}

func (e *h264Encoder) close() {
	if e.mft != 0 {
		com(e.mft, vtProcessMessage, 0x10000001, 0) // NOTIFY_END_STREAMING
		release(e.mft)
		e.mft = 0
	}
}
