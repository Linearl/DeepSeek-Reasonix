//go:build !windows

package main

// publishCDPDebugEndpoint is a no-op outside Windows: the CDP lab switch
// (task 342) only arms the WebView2 browser, which is a Windows-only surface.
// The switch still renders and persists on other platforms so the settings
// stay consistent across sync; it simply has no effect there.
func publishCDPDebugEndpoint() {}
