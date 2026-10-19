package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"testing"
)

func encodedImage(t *testing.T, format string, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.RGBA{R: 1, G: 2, B: 3, A: 255})

	var buf bytes.Buffer
	var err error
	switch format {
	case "png":
		err = png.Encode(&buf, img)
	case "jpeg":
		err = jpeg.Encode(&buf, img, nil)
	case "gif":
		err = gif.Encode(&buf, img, nil)
	default:
		t.Fatalf("unknown format %q", format)
	}
	if err != nil {
		t.Fatalf("encode %s: %v", format, err)
	}
	return buf.Bytes()
}

// riffWrap builds a WebP container around a single chunk, which is all the
// dimension reader ever looks at.
func riffWrap(fourCC string, payload []byte) []byte {
	var b bytes.Buffer
	b.WriteString("RIFF")
	_ = binary.Write(&b, binary.LittleEndian, uint32(12+len(payload)))
	b.WriteString("WEBP")
	b.WriteString(fourCC)
	_ = binary.Write(&b, binary.LittleEndian, uint32(len(payload)))
	b.Write(payload)
	return b.Bytes()
}

func webpVP8(w, h int) []byte {
	payload := make([]byte, 10)
	payload[3], payload[4], payload[5] = 0x9d, 0x01, 0x2a
	binary.LittleEndian.PutUint16(payload[6:8], uint16(w))
	binary.LittleEndian.PutUint16(payload[8:10], uint16(h))
	return riffWrap("VP8 ", payload)
}

func webpVP8L(w, h int) []byte {
	payload := make([]byte, 5)
	payload[0] = 0x2f
	binary.LittleEndian.PutUint32(payload[1:5], uint32(w-1)|uint32(h-1)<<14)
	return riffWrap("VP8L", payload)
}

func webpVP8X(w, h int) []byte {
	payload := make([]byte, 10)
	cw, ch := uint32(w-1), uint32(h-1)
	payload[4], payload[5], payload[6] = byte(cw), byte(cw>>8), byte(cw>>16)
	payload[7], payload[8], payload[9] = byte(ch), byte(ch>>8), byte(ch>>16)
	return riffWrap("VP8X", payload)
}

func TestImageDimensions(t *testing.T) {
	cases := []struct {
		name  string
		data  []byte
		mime  string
		wantW int
		wantH int
	}{
		{"png", encodedImage(t, "png", 640, 480), "image/png", 640, 480},
		{"jpeg", encodedImage(t, "jpeg", 300, 200), "image/jpeg", 300, 200},
		{"gif", encodedImage(t, "gif", 120, 90), "image/gif", 120, 90},

		// The three WebP encodings. VP8L and VP8X both store size minus one, so
		// an off-by-one in either would show up here.
		{"webp lossy", webpVP8(800, 600), "image/webp", 800, 600},
		{"webp lossless", webpVP8L(1024, 768), "image/webp", 1024, 768},
		{"webp extended", webpVP8X(1920, 1080), "image/webp", 1920, 1080},

		// A 14-bit field is the widest a lossy/lossless WebP can express.
		{"webp max 14-bit", webpVP8L(16384, 16384), "image/webp", 16384, 16384},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w, h := imageDimensions(c.data, c.mime)
			if w != c.wantW || h != c.wantH {
				t.Errorf("got %dx%d, want %dx%d", w, h, c.wantW, c.wantH)
			}
		})
	}
}

// Anything unreadable must degrade to (0, 0) — the client then renders exactly
// as it did before dimensions existed — and must never panic, because these
// bytes come straight off an upload.
func TestImageDimensionsUnknownIsZeroNotPanic(t *testing.T) {
	png640 := encodedImage(t, "png", 640, 480)

	cases := []struct {
		name string
		data []byte
		mime string
	}{
		{"empty", nil, "image/png"},
		{"not an image", []byte("this is a text file, not a picture"), "image/png"},
		{"truncated png", png640[:20], "image/png"},
		{"pdf mislabelled as image", []byte("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n"), "image/png"},

		// Every WebP branch, cut short at a different point.
		{"webp empty", nil, "image/webp"},
		{"webp riff only", []byte("RIFF\x00\x00\x00\x00WEBP"), "image/webp"},
		{"webp truncated vp8", webpVP8(800, 600)[:24], "image/webp"},
		{"webp truncated vp8l", webpVP8L(800, 600)[:22], "image/webp"},
		{"webp truncated vp8x", webpVP8X(800, 600)[:25], "image/webp"},
		{"webp bad start code", riffWrap("VP8 ", make([]byte, 10)), "image/webp"},
		{"webp bad signature", riffWrap("VP8L", make([]byte, 5)), "image/webp"},
		{"webp unknown chunk", riffWrap("XXXX", make([]byte, 32)), "image/webp"},
		{"webp not riff", append([]byte("XXXX"), webpVP8(8, 8)[4:]...), "image/webp"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w, h := imageDimensions(c.data, c.mime)
			if w != 0 || h != 0 {
				t.Errorf("got %dx%d, want 0x0", w, h)
			}
		})
	}
}

// A VP8X header can claim up to 2^24 pixels a side. Reserving space for that
// would blank the viewport, so an implausible size is reported as unknown.
func TestImageDimensionsRejectsImplausibleSize(t *testing.T) {
	if w, h := imageDimensions(webpVP8X(16777216, 16777216), "image/webp"); w != 0 || h != 0 {
		t.Errorf("got %dx%d, want 0x0 for an absurd canvas", w, h)
	}
}
