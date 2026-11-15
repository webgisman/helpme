//go:build windows && amd64

package main

import (
	"runtime"
	"testing"
	"time"
)

// Full HD speed: NV12 conversion + H.264 encoding must stay well under 50 ms (20 fps).
func TestH264FullHDSpeed(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	const w, h = 1920, 1080
	enc, err := newH264Encoder(w, h, 20, 4_000_000)
	if err != nil {
		t.Skip(err)
	}
	defer enc.close()
	frame := make([]byte, w*h*4)
	nv := make([]byte, w*h*3/2)
	var conv, total time.Duration
	for f := 0; f < 20; f++ {
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				i := (y*w + x) * 4
				frame[i], frame[i+1], frame[i+2] = byte(x+f*8), byte(y), byte(x^y)
			}
		}
		s := time.Now()
		bgraToNV12(frame, w, h, nv)
		conv += time.Since(s)
		s = time.Now()
		if _, err := enc.encode(frame, w); err != nil {
			t.Fatal(err)
		}
		total += time.Since(s)
	}
	t.Logf("1080p: NV12 conversion %v, conversion+encode %v per frame", conv/20, total/20)
}
