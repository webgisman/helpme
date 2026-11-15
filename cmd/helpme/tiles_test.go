package main

import (
	"bytes"
	"image/jpeg"
	"testing"
)

func TestChangedRects(t *testing.T) {
	w, h := 200, 130
	a := make([]byte, w*h*4)
	b := make([]byte, w*h*4)
	if got := changedRects(a, b, w, h, false); len(got) != 0 {
		t.Fatalf("expected no change, got %v", got)
	}
	full := changedRects(nil, b, w, h, true)
	area := 0
	for _, r := range full {
		area += r.w * r.h
	}
	if area != w*h {
		t.Fatalf("full frame: area %d, want %d", area, w*h)
	}
	// one pixel changed at (150, 70): a single rect, tile (2,1) = x 128..191
	b[(70*w+150)*4] = 255
	got := changedRects(a, b, w, h, false)
	if len(got) != 1 || got[0] != (rect{128, 64, 64, 64}) {
		t.Fatalf("want rect {128 64 64 64}, got %v", got)
	}
	// two neighbouring tiles on the same row are merged
	b[(10*w+10)*4] = 1
	b[(10*w+70)*4] = 1
	got = changedRects(a, b, w, h, false)
	if len(got) != 2 || got[0] != (rect{0, 0, 128, 64}) {
		t.Fatalf("want merged rect, got %v", got)
	}
}

func TestEncodeRectColors(t *testing.T) {
	w, h := 64, 64
	buf := make([]byte, w*h*4)
	for i := 0; i < len(buf); i += 4 {
		buf[i], buf[i+1], buf[i+2] = 255, 0, 0 // BGRA : bleu pur
	}
	jpg, err := encodeRect(buf, w, rect{0, 0, w, h}, 90)
	if err != nil {
		t.Fatal(err)
	}
	img, err := jpeg.Decode(bytes.NewReader(jpg))
	if err != nil {
		t.Fatal(err)
	}
	r, g, b, _ := img.At(10, 10).RGBA()
	if b>>8 < 200 || r>>8 > 60 || g>>8 > 60 {
		t.Fatalf("want blue, got r=%d g=%d b=%d", r>>8, g>>8, b>>8)
	}
}
