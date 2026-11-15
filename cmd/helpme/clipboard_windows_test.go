//go:build windows && amd64

package main

import (
	"testing"
	"time"
)

// Round trip through the real clipboard; the previous text is restored afterwards.
func TestClipboardRoundTrip(t *testing.T) {
	saved := readClipboardText(1 << 20)
	defer func() {
		if saved != "" {
			copyToClipboard(saved)
		}
	}()
	before := clipboardSeq()
	want := "helpme clipboard test ✓ " + time.Now().Format(time.RFC3339Nano)
	if !copyToClipboard(want) {
		t.Skip("clipboard busy")
	}
	if clipboardSeq() == before {
		t.Fatal("sequence number did not change")
	}
	if got := readClipboardText(1 << 20); got != want {
		t.Fatalf("read %q, want %q", got, want)
	}
}
