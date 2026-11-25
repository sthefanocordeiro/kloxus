package web

// Metadata stripping for uploaded images (KLOX-125): a photo pasted into
// a piece must not leak GPS or camera data through its public /m/ URL.
// JPEG drops the APPn (EXIF/XMP/IPTC) and COM segments except the
// APP0 JFIF and APP14 Adobe colour markers; PNG drops the ancillary
// text and eXIf chunks. WebP and GIF pass through unchanged.
import "encoding/binary"

func stripImageMetadata(contentType string, data []byte) []byte {
	switch contentType {
	case "image/jpeg":
		if out, ok := stripJPEG(data); ok {
			return out
		}
	case "image/png":
		if out, ok := stripPNG(data); ok {
			return out
		}
	}
	return data
}

func stripJPEG(d []byte) ([]byte, bool) {
	if len(d) < 4 || d[0] != 0xFF || d[1] != 0xD8 {
		return nil, false
	}
	out := make([]byte, 0, len(d))
	out = append(out, 0xFF, 0xD8)
	i := 2
	for i+4 <= len(d) {
		if d[i] != 0xFF {
			return nil, false
		}
		m := d[i+1]
		if m == 0xFF { // fill byte
			i++
			continue
		}
		if m == 0xDA { // start of scan: the rest is entropy-coded data
			return append(out, d[i:]...), true
		}
		if m >= 0xD0 && m <= 0xD7 || m == 0x01 { // standalone markers
			out = append(out, d[i], d[i+1])
			i += 2
			continue
		}
		n := int(binary.BigEndian.Uint16(d[i+2:]))
		if n < 2 || i+2+n > len(d) {
			return nil, false
		}
		seg := d[i : i+2+n]
		drop := (m >= 0xE1 && m <= 0xED) || m == 0xEF || m == 0xFE
		if !drop {
			out = append(out, seg...)
		}
		i += 2 + n
	}
	return nil, false
}

func stripPNG(d []byte) ([]byte, bool) {
	sig := []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}
	if len(d) < 8 || string(d[:8]) != string(sig) {
		return nil, false
	}
	out := append([]byte{}, sig...)
	i := 8
	for i+12 <= len(d) {
		n := int(binary.BigEndian.Uint32(d[i:]))
		end := i + 12 + n
		if n < 0 || end > len(d) {
			return nil, false
		}
		switch string(d[i+4 : i+8]) {
		case "eXIf", "tEXt", "zTXt", "iTXt", "tIME":
		default:
			out = append(out, d[i:end]...)
		}
		i = end
	}
	return out, i == len(d)
}
