//go:build !windows

package main

import "fmt"

// Non-Windows stub: the tl 1088 clipboard-image write is a Windows syscall
// path; other platforms keep the frontend's navigator.clipboard fallback.
func writeClipboardImageDataURL(dataURL string) error {
	return fmt.Errorf("clipboard image copy is only implemented on Windows (tl 1088)")
}
