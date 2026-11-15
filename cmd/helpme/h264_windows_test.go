//go:build windows && amd64

package main

import (
	"os"
	"runtime"
	"testing"
	"time"
)

// Encodes 30 synthetic frames with the Windows H.264 encoder and checks the Annex B stream.
// Set HELPME_H264_OUT to keep the stream (used by the browser decoding check).
func TestH264Encoder(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	const w, h = 1280, 720
	enc, err := newH264Encoder(w, h, 20, 3_000_000)
	if err != nil {
		t.Skip("encoder unavailable: ", err)
	}
	defer enc.close()
	frame := make([]byte, w*h*4)
	var stream []byte
	start := time.Now()
	for f := 0; f < 30; f++ {
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				i := (y*w + x) * 4
				// left half red, right half blue, plus a moving green bar
				if x < w/2 {
					frame[i], frame[i+1], frame[i+2] = 0, 0, 220
				} else {
					frame[i], frame[i+1], frame[i+2] = 220, 0, 0
				}
				if x >= f*20 && x < f*20+60 {
					frame[i], frame[i+1], frame[i+2] = 0, 200, 0
				}
			}
		}
		out, err := enc.encode(frame, w)
		if err != nil {
			t.Fatal(err)
		}
		stream = append(stream, out...)
	}
	types := map[int]int{}
	for _, n := range nalTypes(stream) {
		types[n]++
	}
	t.Logf("%d bytes, NAL types %v, codec %q, %v per frame", len(stream), types, codecString(stream), time.Since(start)/30)
	if types[7] == 0 || types[8] == 0 || types[5] == 0 || codecString(stream) == "" || !isKeyframe(stream) {
		t.Fatalf("incomplete stream: %v", types)
	}
	if p := os.Getenv("HELPME_H264_OUT"); p != "" {
		os.WriteFile(p, stream, 0o644)
	}
}
