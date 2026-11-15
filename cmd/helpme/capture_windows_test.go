//go:build windows && amd64

package main

import (
	"testing"
	"time"
)

// Real capture of the primary monitor (in memory only) and encoding of a full frame.
func TestCaptureReal(t *testing.T) {
	setDPIAware()
	c, err := newCapturer(monitors()[0])
	if err != nil {
		t.Skip("no screen: ", err)
	}
	defer c.close()
	if err := c.grab(); err != nil {
		t.Skip(err)
	}
	distinct := map[[3]byte]bool{}
	for i := 0; i < len(c.bits) && len(distinct) < 50; i += 4 * 997 {
		distinct[[3]byte{c.bits[i], c.bits[i+1], c.bits[i+2]}] = true
	}
	if len(distinct) < 5 {
		t.Fatalf("almost uniform image (%d colours): empty capture?", len(distinct))
	}
	start := time.Now()
	total := 0
	for _, r := range changedRects(nil, c.bits, c.w, c.h, true) {
		jpg, err := encodeRect(c.bits, c.w, r, 70)
		if err != nil {
			t.Fatal(err)
		}
		total += len(jpg)
	}
	t.Logf("screen %dx%d, full frame %d KB as JPEG, encoded in %v", c.w, c.h, total/1024, time.Since(start))
}
