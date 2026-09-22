//go:build !windows

package builtin

import "fmt"

// captureWindowScreenshot is the non-Windows stub of the screenshot tool
// (task 233 batch 1): it fails explicitly instead of silently degrading, so
// an agent on an unsupported platform gets a repairable error rather than a
// fake result.
func captureWindowScreenshot(outputPath, titleSubstring string) (w, h int, title string, err error) {
	return 0, 0, "", fmt.Errorf("window capture is only implemented on Windows (task 233 batch 1); requested output %s", outputPath)
}
