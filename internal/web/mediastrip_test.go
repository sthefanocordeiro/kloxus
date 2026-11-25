package web

import (
	"bytes"
	"testing"
)

func TestStripJPEGRemovesExif(t *testing.T) {
	jfif := []byte{0xFF, 0xE0, 0x00, 0x04, 'J', 'F'}
	exif := []byte{0xFF, 0xE1, 0x00, 0x08, 'E', 'x', 'i', 'f', 0, 0}
	dqt := []byte{0xFF, 0xDB, 0x00, 0x04, 1, 2}
	sos := []byte{0xFF, 0xDA, 0x00, 0x02, 0xAA, 0xBB, 0xFF, 0xD9}
	in := bytes.Join([][]byte{{0xFF, 0xD8}, jfif, exif, dqt, sos}, nil)
	out := stripImageMetadata("image/jpeg", in)
	want := bytes.Join([][]byte{{0xFF, 0xD8}, jfif, dqt, sos}, nil)
	if !bytes.Equal(out, want) {
		t.Fatalf("jpeg strip:\n% x\nwant\n% x", out, want)
	}
}

func pngChunk(typ string, data []byte) []byte {
	b := []byte{0, 0, 0, byte(len(data))}
	b = append(b, typ...)
	b = append(b, data...)
	return append(b, 0, 0, 0, 0) // CRC is not checked by the stripper
}

func TestStripPNGRemovesTextAndExif(t *testing.T) {
	sig := []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}
	ihdr, idat, iend := pngChunk("IHDR", make([]byte, 13)), pngChunk("IDAT", []byte{1, 2}), pngChunk("IEND", nil)
	in := bytes.Join([][]byte{sig, ihdr, pngChunk("eXIf", []byte("gps")), pngChunk("tEXt", []byte("a\x00b")), idat, iend}, nil)
	want := bytes.Join([][]byte{sig, ihdr, idat, iend}, nil)
	if out := stripImageMetadata("image/png", in); !bytes.Equal(out, want) {
		t.Fatalf("png strip mismatch:\n% x\nwant\n% x", out, want)
	}
	// malformed input passes through untouched rather than being corrupted
	junk := []byte("not an image")
	if out := stripImageMetadata("image/png", junk); !bytes.Equal(out, junk) {
		t.Fatal("malformed png must pass through")
	}
}
