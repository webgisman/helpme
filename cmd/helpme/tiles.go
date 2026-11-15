package main

import (
	"bytes"
	"image"
	"image/jpeg"
)

// Tiles mode: the screen is split into 64x64 tiles and only the tiles that changed since the previous
// frame are sent, merged into horizontal runs, as JPEG. For remote help (menus, windows, text) this is
// very cheap and keeps text sharp.

const tileSize = 64

type rect struct{ x, y, w, h int }

// changedRects compares two BGRA frames of the same size and returns the changed areas.
func changedRects(prev, cur []byte, w, h int, full bool) []rect {
	if full || len(prev) != len(cur) {
		return bands(w, h, func(tx, ty int) bool { return true })
	}
	stride := w * 4
	return bands(w, h, func(tx, ty int) bool {
		x0, y0 := tx*tileSize, ty*tileSize
		x1, y1 := min(x0+tileSize, w), min(y0+tileSize, h)
		for y := y0; y < y1; y++ {
			o := y*stride + x0*4
			if !bytes.Equal(prev[o:o+(x1-x0)*4], cur[o:o+(x1-x0)*4]) {
				return true
			}
		}
		return false
	})
}

// bands merges adjacent changed tiles of a row into a single rect.
func bands(w, h int, changed func(tx, ty int) bool) []rect {
	var out []rect
	tw, th := (w+tileSize-1)/tileSize, (h+tileSize-1)/tileSize
	for ty := 0; ty < th; ty++ {
		start := -1
		for tx := 0; tx <= tw; tx++ {
			on := tx < tw && changed(tx, ty)
			if on && start < 0 {
				start = tx
			}
			if !on && start >= 0 {
				x0, y0 := start*tileSize, ty*tileSize
				out = append(out, rect{x0, y0, min(tx*tileSize, w) - x0, min(y0+tileSize, h) - y0})
				start = -1
			}
		}
	}
	return out
}

// encodeRect encodes a BGRA area as JPEG.
func encodeRect(buf []byte, w int, r rect, quality int) ([]byte, error) {
	img := image.NewRGBA(image.Rect(0, 0, r.w, r.h))
	stride := w * 4
	for y := 0; y < r.h; y++ {
		src := buf[(r.y+y)*stride+r.x*4 : (r.y+y)*stride+(r.x+r.w)*4]
		dst := img.Pix[y*img.Stride : y*img.Stride+r.w*4]
		for i := 0; i < len(src); i += 4 {
			dst[i], dst[i+1], dst[i+2], dst[i+3] = src[i+2], src[i+1], src[i], 255
		}
	}
	var out bytes.Buffer
	if err := jpeg.Encode(&out, img, &jpeg.Options{Quality: quality}); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
