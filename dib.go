package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
)

// DIB (device-independent bitmap) <-> image conversion for the Windows
// clipboard formats CF_DIB / CF_DIBV5. Platform-independent so it can be
// unit-tested on Linux. Supports BI_RGB at 24/32 bpp.

// ---- DIB <-> image conversion (BI_RGB, 24/32 bpp) ----

func dibToImage(dib []byte) (image.Image, error) {
	if len(dib) < 40 {
		return nil, errors.New("DIB too small")
	}
	headerSize := int(binary.LittleEndian.Uint32(dib[0:]))
	if headerSize < 40 || len(dib) < headerSize {
		return nil, errors.New("bad DIB header size")
	}
	width := int(int32(binary.LittleEndian.Uint32(dib[4:])))
	height := int(int32(binary.LittleEndian.Uint32(dib[8:])))
	planes := binary.LittleEndian.Uint16(dib[12:])
	bitCount := int(binary.LittleEndian.Uint16(dib[14:]))
	compression := binary.LittleEndian.Uint32(dib[16:])
	clrUsed := int(binary.LittleEndian.Uint32(dib[32:]))
	if planes != 1 || width <= 0 || height == 0 {
		return nil, errors.New("bad DIB dimensions")
	}
	if compression != 0 {
		return nil, fmt.Errorf("unsupported DIB compression %d", compression)
	}
	if bitCount != 24 && bitCount != 32 {
		return nil, fmt.Errorf("unsupported DIB bit count %d", bitCount)
	}
	topDown := height < 0
	h := height
	if h < 0 {
		h = -h
	}
	paletteBytes := 0
	if bitCount <= 8 {
		n := clrUsed
		if n == 0 {
			n = 1 << bitCount
		}
		paletteBytes = n * 4
	}
	offset := headerSize + paletteBytes
	stride := ((width*bitCount + 31) / 32) * 4
	if len(dib) < offset+stride*h {
		return nil, errors.New("DIB pixel data truncated")
	}
	bytesPerPx := bitCount / 8

	// Some 32bpp sources write alpha=0 everywhere; treat that as opaque.
	alphaAllZero := true
	if bitCount == 32 {
		for y := 0; y < h && alphaAllZero; y++ {
			row := offset + y*stride
			for x := 0; x < width; x++ {
				if dib[row+x*4+3] != 0 {
					alphaAllZero = false
					break
				}
			}
		}
	}

	img := image.NewRGBA(image.Rect(0, 0, width, h))
	for y := 0; y < h; y++ {
		srcY := y
		if !topDown {
			srcY = h - 1 - y
		}
		row := offset + srcY*stride
		for x := 0; x < width; x++ {
			p := row + x*bytesPerPx
			b, g, r := dib[p], dib[p+1], dib[p+2]
			a := byte(0xFF)
			if bitCount == 32 && !alphaAllZero {
				a = dib[p+3]
			}
			img.SetRGBA(x, y, color.RGBA{R: r, G: g, B: b, A: a})
		}
	}
	return img, nil
}

func imageToDIB(img image.Image) ([]byte, error) {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return nil, errors.New("empty image")
	}
	stride := w * 4 // 32bpp rows are always 4-byte aligned
	sizeImage := stride * h
	dib := make([]byte, 40+sizeImage)
	binary.LittleEndian.PutUint32(dib[0:], 40)
	binary.LittleEndian.PutUint32(dib[4:], uint32(int32(w)))
	binary.LittleEndian.PutUint32(dib[8:], uint32(int32(h))) // positive = bottom-up
	binary.LittleEndian.PutUint16(dib[12:], 1)
	binary.LittleEndian.PutUint16(dib[14:], 32)
	binary.LittleEndian.PutUint32(dib[16:], 0) // BI_RGB
	binary.LittleEndian.PutUint32(dib[20:], uint32(sizeImage))
	for y := 0; y < h; y++ {
		dstY := h - 1 - y // bottom-up
		row := 40 + dstY*stride
		for x := 0; x < w; x++ {
			r, g, bl, a := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
			p := row + x*4
			dib[p] = byte(bl >> 8)
			dib[p+1] = byte(g >> 8)
			dib[p+2] = byte(r >> 8)
			dib[p+3] = byte(a >> 8)
		}
	}
	return dib, nil
}
