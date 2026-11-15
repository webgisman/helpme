package main

import "fmt"

// nalTypes returns the NAL unit types found in an Annex B H.264 stream.
func nalTypes(b []byte) []int {
	var out []int
	for i := 0; i+3 < len(b); i++ {
		if b[i] == 0 && b[i+1] == 0 && (b[i+2] == 1 || (b[i+2] == 0 && i+4 < len(b) && b[i+3] == 1)) {
			j := i + 3
			if b[i+2] == 0 {
				j = i + 4
			}
			if j < len(b) {
				out = append(out, int(b[j]&0x1f))
			}
			i = j
		}
	}
	return out
}

// isKeyframe reports whether the chunk contains an IDR slice.
func isKeyframe(b []byte) bool {
	for _, t := range nalTypes(b) {
		if t == 5 {
			return true
		}
	}
	return false
}

// codecString builds the WebCodecs codec string ("avc1.PPCCLL") from the SPS, if present.
func codecString(b []byte) string {
	for i := 0; i+7 < len(b); i++ {
		if b[i] == 0 && b[i+1] == 0 && b[i+2] == 1 && b[i+3]&0x1f == 7 {
			return fmt.Sprintf("avc1.%02X%02X%02X", b[i+4], b[i+5], b[i+6])
		}
	}
	return ""
}

// bgraToNV12 converts a BGRA frame (w and h even) to NV12, BT.601 limited range.
func bgraToNV12(src []byte, w, h int, dst []byte) {
	uv := dst[w*h:]
	for y := 0; y < h; y += 2 {
		r0 := src[y*w*4:]
		r1 := src[(y+1)*w*4:]
		y0 := dst[y*w:]
		y1 := dst[(y+1)*w:]
		c := uv[(y/2)*w:]
		for x := 0; x < w; x += 2 {
			var sr, sg, sb int
			for k, p := range [4][]byte{r0[x*4:], r0[x*4+4:], r1[x*4:], r1[x*4+4:]} {
				b, g, r := int(p[0]), int(p[1]), int(p[2])
				yy := byte(((66*r + 129*g + 25*b + 128) >> 8) + 16)
				switch k {
				case 0:
					y0[x] = yy
				case 1:
					y0[x+1] = yy
				case 2:
					y1[x] = yy
				default:
					y1[x+1] = yy
				}
				sr, sg, sb = sr+r, sg+g, sb+b
			}
			sr, sg, sb = sr/4, sg/4, sb/4
			c[x] = byte(((-38*sr - 74*sg + 112*sb + 128) >> 8) + 128)
			c[x+1] = byte(((112*sr - 94*sg - 18*sb + 128) >> 8) + 128)
		}
	}
}
