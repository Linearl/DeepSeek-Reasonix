//go:build !windows

package main

import "fmt"

// Non-Windows stubs for the tl 1087/1088 desktop screenshot capture: they
// fail explicitly instead of silently degrading, mirroring the task-233
// screenshot tool's cross-platform contract.

type ScreenMetrics struct {
	Width  int `json:"width"`
	Height int `json:"height"`
	X      int `json:"x"`
	Y      int `json:"y"`
}

func captureScreenAtCursor(outputPath string) (w, h, originX, originY int, err error) {
	return 0, 0, 0, 0, fmt.Errorf("screen capture is only implemented on Windows (tl 1087); requested output %s", outputPath)
}

func screenMetricsAtCursor() (ScreenMetrics, error) {
	return ScreenMetrics{}, fmt.Errorf("screen capture is only implemented on Windows (tl 1087)")
}

func virtualScreenMetrics() (ScreenMetrics, error) {
	return ScreenMetrics{}, fmt.Errorf("screen capture is only implemented on Windows (tl 1088)")
}

func captureVirtualScreenPNG(outputPath string) (ScreenMetrics, error) {
	return ScreenMetrics{}, fmt.Errorf("screen capture is only implemented on Windows (tl 1088)")
}
