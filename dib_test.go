package main

import (
	"encoding/binary"
	"image"
	"image/color"
	"testing"
)

func testImage(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, color.RGBA{
				R: uint8((x*47 + y*13) % 256),
				G: uint8((x*7 + y*91) % 256),
				B: uint8((x*31 + y*53) % 256),
				A: 0xFF,
			})
		}
	}
	return img
}

func pixelsEqual(t *testing.T, a, b image.Image) {
	t.Helper()
	ba, bb := a.Bounds(), b.Bounds()
	if ba.Dx() != bb.Dx() || ba.Dy() != bb.Dy() {
		t.Fatalf("size mismatch %v vs %v", ba, bb)
	}
	for y := 0; y < ba.Dy(); y++ {
		for x := 0; x < ba.Dx(); x++ {
			r1, g1, b1, _ := a.At(ba.Min.X+x, ba.Min.Y+y).RGBA()
			r2, g2, b2, _ := b.At(bb.Min.X+x, bb.Min.Y+y).RGBA()
			if r1 != r2 || g1 != g2 || b1 != b2 {
				t.Fatalf("pixel (%d,%d): %x,%x,%x vs %x,%x,%x", x, y, r1, g1, b1, r2, g2, b2)
			}
		}
	}
}

func TestDIBRoundTrip32(t *testing.T) {
	for _, size := range [][2]int{{1, 1}, {2, 2}, {5, 3}, {17, 9}} {
		src := testImage(size[0], size[1])
		dib, err := imageToDIB(src)
		if err != nil {
			t.Fatal(err)
		}
		back, err := dibToImage(dib)
		if err != nil {
			t.Fatal(err)
		}
		pixelsEqual(t, src, back)
		if pixelHash(src) != pixelHash(back) {
			t.Fatalf("pixel hash differs after round trip %v", size)
		}
	}
}

// Build a 24bpp bottom-up DIB by hand (odd width exercises row padding)
// and check orientation + colors.
func TestDIBRead24BottomUp(t *testing.T) {
	w, h := 3, 2
	stride := ((w*24 + 31) / 32) * 4 // 12
	dib := make([]byte, 40+stride*h)
	binary.LittleEndian.PutUint32(dib[0:], 40)
	binary.LittleEndian.PutUint32(dib[4:], uint32(int32(w)))
	binary.LittleEndian.PutUint32(dib[8:], uint32(int32(h))) // bottom-up
	binary.LittleEndian.PutUint16(dib[12:], 1)
	binary.LittleEndian.PutUint16(dib[14:], 24)
	// Top row (y=0) should be red, green, blue; bottom row white, black, gray.
	// Stored bottom-up: first stored row = bottom row.
	setPx := func(storedRow, x int, r, g, b byte) {
		p := 40 + storedRow*stride + x*3
		dib[p], dib[p+1], dib[p+2] = b, g, r
	}
	setPx(0, 0, 255, 255, 255)
	setPx(0, 1, 0, 0, 0)
	setPx(0, 2, 128, 128, 128)
	setPx(1, 0, 255, 0, 0)
	setPx(1, 1, 0, 255, 0)
	setPx(1, 2, 0, 0, 255)
	img, err := dibToImage(dib)
	if err != nil {
		t.Fatal(err)
	}
	checks := map[[2]int]color.RGBA{
		{0, 0}: {255, 0, 0, 255}, {1, 0}: {0, 255, 0, 255}, {2, 0}: {0, 0, 255, 255},
		{0, 1}: {255, 255, 255, 255}, {1, 1}: {0, 0, 0, 255}, {2, 1}: {128, 128, 128, 255},
	}
	for pos, want := range checks {
		got := color.RGBAModel.Convert(img.At(pos[0], pos[1])).(color.RGBA)
		if got != want {
			t.Fatalf("(%d,%d): got %v want %v", pos[0], pos[1], got, want)
		}
	}
}

func TestDIBRead32AlphaAllZeroIsOpaque(t *testing.T) {
	src := testImage(2, 1)
	dib, err := imageToDIB(src)
	if err != nil {
		t.Fatal(err)
	}
	// Zero out all alpha bytes, like some apps do.
	for i := 40 + 3; i < len(dib); i += 4 {
		dib[i] = 0
	}
	back, err := dibToImage(dib)
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, a := back.At(0, 0).RGBA()
	if a != 0xFFFF {
		t.Fatalf("alpha should be forced opaque, got %x", a)
	}
	pixelsEqual(t, src, back)
}
