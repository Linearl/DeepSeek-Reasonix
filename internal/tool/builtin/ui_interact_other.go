//go:build !windows

package builtin

import "fmt"

// The non-Windows stub of the ui_interact driver (task 233 batch 2): it fails
// explicitly instead of silently degrading, so an agent on an unsupported
// platform gets a repairable error rather than a fake result.
func uiDriverActivate(string) (string, error) {
	return "", fmt.Errorf("ui_interact is only implemented on Windows (task 233 batch 2)")
}

func uiDriverClick(string, int, int, bool, bool) (string, error) {
	return "", fmt.Errorf("ui_interact is only implemented on Windows (task 233 batch 2)")
}

func uiDriverType(string, string) (string, error) {
	return "", fmt.Errorf("ui_interact is only implemented on Windows (task 233 batch 2)")
}

func uiDriverKey(string, uint16, []uint16) (string, error) {
	return "", fmt.Errorf("ui_interact is only implemented on Windows (task 233 batch 2)")
}
