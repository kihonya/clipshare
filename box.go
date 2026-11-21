package main

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	maxTextBytes  = 1 << 20  // 1 MiB
	maxImageBytes = 10 << 20 // 10 MiB (decoded)
	maxBodyBytes  = 15 << 20 // 15 MiB (JSON body)
)

// Entry is the single item held by the box. Data is the transport form:
// UTF-8 bytes for text, PNG bytes for images.
type Entry struct {
	Type        string
	Data        []byte
	Mime        string
	Origin      string // "iphone" | "windows"
	Version     int64
	ContentHash string
	UpdatedAt   time.Time
}

// Box holds only the latest entry, in memory only (never written to disk).
type Box struct {
	mu       sync.Mutex
	entry    *Entry
	version  int64 // monotonic across clears, so guardians never confuse an old and a new entry
	clearGen int64 // incremented on every clear
}

func contentHashText(b []byte) string {
	h := sha256.New()
	h.Write([]byte("text\x00"))
	h.Write(b)
	return hex.EncodeToString(h.Sum(nil))
}

// pixelHash hashes decoded pixels + dimensions so the same picture keeps the
// same hash even after a PNG<->DIB round trip on Windows.
func pixelHash(img image.Image) string {
	b := img.Bounds()
	w, hgt := b.Dx(), b.Dy()
	h := sha256.New()
	fmt.Fprintf(h, "image\x00%dx%d\x00", w, hgt)
	buf := make([]byte, 0, 4096)
	for y := 0; y < hgt; y++ {
		for x := 0; x < w; x++ {
			r, g, bl, a := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
			buf = append(buf, byte(r>>8), byte(g>>8), byte(bl>>8), byte(a>>8))
			if len(buf) >= 4096 {
				h.Write(buf)
				buf = buf[:0]
			}
		}
	}
	if len(buf) > 0 {
		h.Write(buf)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// normalizeImage decodes png/jpeg/gif bytes and returns canonical PNG bytes
// plus the pixel hash.
func normalizeImage(raw []byte) (pngBytes []byte, hash string, err error) {
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, "", err
	}
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		return nil, "", err
	}
	return out.Bytes(), pixelHash(img), nil
}

// store inserts a new entry unless identical content is already in the box.
// Returns the resulting entry and whether it was newly stored.
func (b *Box) store(typ string, data []byte, mime, origin, hash string) (*Entry, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.entry != nil && b.entry.ContentHash == hash {
		return b.entry, false
	}
	b.version++
	v := b.version
	cp := make([]byte, len(data))
	copy(cp, data)
	b.entry = &Entry{
		Type: typ, Data: cp, Mime: mime, Origin: origin,
		Version: v, ContentHash: hash, UpdatedAt: time.Now(),
	}
	return b.entry, true
}

// clear empties the box and bumps the clear generation so the guardian
// can reset its dedupe state.
func (b *Box) clear() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.entry = nil
	b.clearGen++
}

func (b *Box) clearGeneration() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.clearGen
}

func (b *Box) snapshot() *Entry {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.entry == nil {
		return nil
	}
	e := *b.entry
	return &e
}

// ---- HTTP ----

type clipJSON struct {
	Type      string `json:"type"`
	Data      string `json:"data"`
	Mime      string `json:"mime,omitempty"`
	Origin    string `json:"origin,omitempty"`
	Version   int64  `json:"version,omitempty"`
	Hash      string `json:"hash,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

func entryToJSON(e *Entry) clipJSON {
	j := clipJSON{
		Type: e.Type, Mime: e.Mime, Origin: e.Origin,
		Version: e.Version, Hash: e.ContentHash,
		UpdatedAt: e.UpdatedAt.Format(time.RFC3339),
	}
	if e.Type == "image" {
		j.Data = base64.StdEncoding.EncodeToString(e.Data)
	} else {
		j.Data = string(e.Data)
	}
	return j
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func authMiddleware(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := ""
		if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
			got = strings.TrimPrefix(h, "Bearer ")
		} else if h := r.Header.Get("X-Clip-Token"); h != "" {
			got = h
		}
		if got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func newMux(box *Box, token string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
	})
	// Web UI (no auth: the page itself holds no secret; the token arrives
	// via the ?k= query parameter and is only used for API calls).
	mux.HandleFunc("GET /{$}", pageHandler)
	mux.HandleFunc("GET /app", pageHandler)

	clip := http.NewServeMux()
	clip.HandleFunc("POST /clip", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
		if err != nil {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{"error": "body too large"})
			return
		}
		var req clipJSON
		if err := json.Unmarshal(body, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
			return
		}
		origin := req.Origin
		if origin != "iphone" && origin != "windows" {
			origin = "iphone" // forgiving default for Shortcuts
		}
		switch req.Type {
		case "text":
			data := []byte(req.Data)
			if len(data) > maxTextBytes {
				writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{"error": "text too large"})
				return
			}
			e, _ := box.store("text", data, "text/plain", origin, contentHashText(data))
			writeJSON(w, http.StatusOK, map[string]any{
				"ok": true, "version": e.Version, "hash": e.ContentHash,
				"updated_at": e.UpdatedAt.Format(time.RFC3339),
			})
		case "image":
			cleaned := strings.Join(strings.Fields(req.Data), "")
			raw, err := base64.StdEncoding.DecodeString(cleaned)
			if err != nil {
				raw, err = base64.RawStdEncoding.DecodeString(cleaned)
				if err != nil {
					writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid base64"})
					return
				}
			}
			if len(raw) > maxImageBytes {
				writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{"error": "image too large"})
				return
			}
			pngBytes, hash, err := normalizeImage(raw)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": "unsupported image"})
				return
			}
			e, _ := box.store("image", pngBytes, "image/png", origin, hash)
			writeJSON(w, http.StatusOK, map[string]any{
				"ok": true, "version": e.Version, "hash": e.ContentHash,
				"updated_at": e.UpdatedAt.Format(time.RFC3339),
			})
		default:
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "type must be text or image"})
		}
	})
	clip.HandleFunc("GET /clip", func(w http.ResponseWriter, r *http.Request) {
		e := box.snapshot()
		if e == nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"empty": true})
			return
		}
		writeJSON(w, http.StatusOK, entryToJSON(e))
	})
	clip.HandleFunc("DELETE /clip", func(w http.ResponseWriter, r *http.Request) {
		box.clear()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "cleared": true})
	})
	clip.HandleFunc("GET /clip/meta", func(w http.ResponseWriter, r *http.Request) {
		e := box.snapshot()
		if e == nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"empty": true})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"version": e.Version, "hash": e.ContentHash, "type": e.Type,
			"origin": e.Origin, "updated_at": e.UpdatedAt.Format(time.RFC3339),
			"size": len(e.Data),
		})
	})
	mux.Handle("/", authMiddleware(token, clip))
	return mux
}
