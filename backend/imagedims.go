package main

import (
	"bytes"
	"encoding/binary"
	"image"
	// Registered for their DecodeConfig side effect only — the pixel data is
	// never decoded here.
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
)

// maxReasonableImageDimension guards against a corrupt or hostile header
// claiming an enormous canvas. The value only ever reaches the browser as an
// aspect-ratio hint, but a wild number there would reserve a screenful of
// blank space, so an implausible size is treated as "unknown" instead.
const maxReasonableImageDimension = 100000

// imageDimensions returns the pixel width and height of an encoded image, or
// (0, 0) when they cannot be determined — an unsupported format, a truncated
// upload, or a file that is not an image at all.
//
// The frontend needs these to reserve the right box before the picture itself
// arrives. Feed images load lazily, so without a known aspect ratio the page
// grows as each one lands and shoves the reader's position around.
//
// Only the header is parsed, never the pixel data, so this stays cheap on a
// large upload and cannot be made to allocate a full-size framebuffer.
func imageDimensions(data []byte, mime string) (int, int) {
	var w, h int
	if mime == "image/webp" {
		// The standard library has no WebP decoder, and golang.org/x/image is a
		// large dependency to add for three integers.
		w, h = webpDimensions(data)
	} else {
		cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			return 0, 0
		}
		w, h = cfg.Width, cfg.Height
	}

	if w <= 0 || h <= 0 || w > maxReasonableImageDimension || h > maxReasonableImageDimension {
		return 0, 0
	}
	return w, h
}

// webpDimensions reads the canvas size out of a WebP file's RIFF header.
//
// The container is "RIFF" + a 4-byte length + "WEBP", followed by chunks of a
// 4-byte FourCC, a 4-byte little-endian payload length, and the payload. The
// first chunk always carries the dimensions, in one of three encodings.
func webpDimensions(data []byte) (int, int) {
	if len(data) < 20 || string(data[0:4]) != "RIFF" || string(data[8:12]) != "WEBP" {
		return 0, 0
	}
	// Payload of the first chunk: 12 bytes of RIFF header, then this chunk's
	// own 4-byte FourCC and 4-byte length.
	body := data[20:]

	switch string(data[12:16]) {
	case "VP8X":
		// Extended (also what animated files use): a 4-byte flags field, then
		// canvas width and height as 24-bit little-endian values, each stored
		// minus one.
		if len(body) < 10 {
			return 0, 0
		}
		w := int(body[4]) | int(body[5])<<8 | int(body[6])<<16
		h := int(body[7]) | int(body[8])<<8 | int(body[9])<<16
		return w + 1, h + 1

	case "VP8 ":
		// Lossy: a 3-byte frame tag, the 3-byte start code 9d 01 2a, then
		// width and height as 16-bit little-endian values whose top two bits
		// are a scaling hint rather than part of the size.
		if len(body) < 10 || body[3] != 0x9d || body[4] != 0x01 || body[5] != 0x2a {
			return 0, 0
		}
		w := int(binary.LittleEndian.Uint16(body[6:8]) & 0x3fff)
		h := int(binary.LittleEndian.Uint16(body[8:10]) & 0x3fff)
		return w, h

	case "VP8L":
		// Lossless: a 0x2f signature byte, then 14 bits of width-1 followed by
		// 14 bits of height-1, packed little-endian into the next 4 bytes.
		if len(body) < 5 || body[0] != 0x2f {
			return 0, 0
		}
		bits := binary.LittleEndian.Uint32(body[1:5])
		return int(bits&0x3fff) + 1, int((bits>>14)&0x3fff) + 1
	}
	return 0, 0
}
