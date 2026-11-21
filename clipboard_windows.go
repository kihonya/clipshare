//go:build windows

package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image/png"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"
)

// Minimal Win32 clipboard access via stdlib syscall only (no cgo, no deps),
// so the binary cross-compiles to a single static .exe.

const (
	cfText        = 1
	cfBitmap      = 2
	cfDIB         = 8
	cfUnicodeText = 13
	cfDIBV5       = 17

	gmemMoveable = 0x0002
)

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")

	procOpenClipboard            = user32.NewProc("OpenClipboard")
	procCloseClipboard           = user32.NewProc("CloseClipboard")
	procEmptyClipboard           = user32.NewProc("EmptyClipboard")
	procIsClipboardFormatAvail   = user32.NewProc("IsClipboardFormatAvailable")
	procGetClipboardData         = user32.NewProc("GetClipboardData")
	procSetClipboardData         = user32.NewProc("SetClipboardData")
	procGetClipboardSequenceNum  = user32.NewProc("GetClipboardSequenceNumber")
	procRegisterClipboardFormatW = user32.NewProc("RegisterClipboardFormatW")

	procGlobalAlloc  = kernel32.NewProc("GlobalAlloc")
	procGlobalFree   = kernel32.NewProc("GlobalFree")
	procGlobalLock   = kernel32.NewProc("GlobalLock")
	procGlobalUnlock = kernel32.NewProc("GlobalUnlock")
	procGlobalSize   = kernel32.NewProc("GlobalSize")
)

var pngFormatID uintptr

func init() {
	name, err := syscall.UTF16PtrFromString("PNG")
	if err == nil {
		r, _, _ := procRegisterClipboardFormatW.Call(uintptr(unsafe.Pointer(name)))
		pngFormatID = r
	}
}

func clipboardSeq() uint32 {
	r, _, _ := procGetClipboardSequenceNum.Call()
	return uint32(r)
}

func openClipboardRetry() error {
	for i := 0; i < 20; i++ {
		r, _, _ := procOpenClipboard.Call(0)
		if r != 0 {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return errors.New("OpenClipboard failed (held by another app)")
}

func closeClipboard() { procCloseClipboard.Call() }

func formatAvailable(format uintptr) bool {
	r, _, _ := procIsClipboardFormatAvail.Call(format)
	return r != 0
}

func globalAllocCopy(data []byte) (uintptr, error) {
	h, _, e := procGlobalAlloc.Call(gmemMoveable, uintptr(len(data)))
	if h == 0 {
		return 0, fmt.Errorf("GlobalAlloc: %v", e)
	}
	p, _, e := procGlobalLock.Call(h)
	if p == 0 {
		procGlobalFree.Call(h)
		return 0, fmt.Errorf("GlobalLock: %v", e)
	}
	dst := unsafe.Slice((*byte)(unsafe.Pointer(p)), len(data))
	copy(dst, data)
	procGlobalUnlock.Call(h)
	return h, nil
}

func globalRead(h uintptr) ([]byte, error) {
	sz, _, _ := procGlobalSize.Call(h)
	if sz == 0 {
		return nil, errors.New("GlobalSize returned 0")
	}
	p, _, e := procGlobalLock.Call(h)
	if p == 0 {
		return nil, fmt.Errorf("GlobalLock: %v", e)
	}
	src := unsafe.Slice((*byte)(unsafe.Pointer(p)), int(sz))
	out := make([]byte, len(src))
	copy(out, src)
	procGlobalUnlock.Call(h)
	return out, nil
}

func readClipboard() (*clipContent, bool, error) {
	if err := openClipboardRetry(); err != nil {
		return nil, false, err
	}
	defer closeClipboard()

	// Text first: plain-text copies set CF_UNICODETEXT; image copies from
	// browsers/apps set DIB/PNG without plain text, so this order is safe.
	if formatAvailable(cfUnicodeText) {
		h, _, _ := procGetClipboardData.Call(cfUnicodeText)
		if h != 0 {
			raw, err := globalRead(h)
			if err == nil && len(raw) >= 2 {
				u16 := make([]uint16, 0, len(raw)/2)
				for i := 0; i+1 < len(raw); i += 2 {
					v := binary.LittleEndian.Uint16(raw[i:])
					if v == 0 {
						break
					}
					u16 = append(u16, v)
				}
				s := string(utf16.Decode(u16))
				if s != "" {
					return &clipContent{
						Type: "text", Text: s,
						ContentHash: contentHashText([]byte(s)),
					}, true, nil
				}
			}
		}
	}

	// PNG registered format (browsers and modern apps provide it).
	if pngFormatID != 0 && formatAvailable(pngFormatID) {
		h, _, _ := procGetClipboardData.Call(pngFormatID)
		if h != 0 {
			raw, err := globalRead(h)
			if err == nil && len(raw) > 0 {
				if pngBytes, hash, err := normalizeImage(raw); err == nil {
					return &clipContent{Type: "image", PNG: pngBytes, ContentHash: hash}, true, nil
				}
			}
		}
	}

	// DIB / DIBV5.
	for _, format := range []uintptr{cfDIB, cfDIBV5} {
		if formatAvailable(format) {
			h, _, _ := procGetClipboardData.Call(format)
			if h == 0 {
				continue
			}
			raw, err := globalRead(h)
			if err != nil {
				continue
			}
			img, err := dibToImage(raw)
			if err != nil {
				continue
			}
			var buf bytes.Buffer
			if err := png.Encode(&buf, img); err != nil {
				continue
			}
			return &clipContent{
				Type: "image", PNG: buf.Bytes(),
				ContentHash: pixelHash(img),
			}, true, nil
		}
	}
	return nil, false, nil
}

func writeClipboard(c *clipContent) error {
	if err := openClipboardRetry(); err != nil {
		return err
	}
	defer closeClipboard()
	procEmptyClipboard.Call()

	switch c.Type {
	case "text":
		u16 := utf16.Encode([]rune(c.Text))
		buf := make([]byte, 0, (len(u16)+1)*2)
		tmp := make([]byte, 2)
		for _, v := range u16 {
			binary.LittleEndian.PutUint16(tmp, v)
			buf = append(buf, tmp...)
		}
		buf = append(buf, 0, 0)
		h, err := globalAllocCopy(buf)
		if err != nil {
			return err
		}
		r, _, e := procSetClipboardData.Call(cfUnicodeText, h)
		if r == 0 {
			procGlobalFree.Call(h)
			return fmt.Errorf("SetClipboardData(text): %v", e)
		}
		return nil
	case "image":
		img, err := png.Decode(bytes.NewReader(c.PNG))
		if err != nil {
			return err
		}
		dib, err := imageToDIB(img)
		if err != nil {
			return err
		}
		h, err := globalAllocCopy(dib)
		if err != nil {
			return err
		}
		r, _, e := procSetClipboardData.Call(cfDIB, h)
		if r == 0 {
			procGlobalFree.Call(h)
			return fmt.Errorf("SetClipboardData(DIB): %v", e)
		}
		// Also offer the PNG format for apps that prefer it. Best effort.
		if pngFormatID != 0 {
			if h2, err := globalAllocCopy(c.PNG); err == nil {
				if r2, _, _ := procSetClipboardData.Call(pngFormatID, h2); r2 == 0 {
					procGlobalFree.Call(h2)
				}
			}
		}
		return nil
	}
	return fmt.Errorf("unknown type %q", c.Type)
}
