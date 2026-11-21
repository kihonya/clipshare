package main

// clipContent is one clipboard payload in canonical form.
// Text: Text holds the string. Image: PNG holds canonical PNG bytes.
// ContentHash matches the Box hashing scheme (see box.go).
// Platform implementations live in clipboard_windows.go / clipboard_other.go
// and provide readClipboard, writeClipboard and clipboardSeq; they are
// called only from the guardian goroutine.
type clipContent struct {
	Type        string // "text" | "image"
	Text        string
	PNG         []byte
	ContentHash string
}
