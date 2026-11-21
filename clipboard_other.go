//go:build !windows

package main

// Stub for non-Windows builds: lets the box (HTTP server) be exercised on
// Linux during development. The guardian simply idles.

func readClipboard() (*clipContent, bool, error) {
	return nil, false, nil
}

func writeClipboard(c *clipContent) error {
	return nil
}

func clipboardSeq() uint32 {
	return 0
}
