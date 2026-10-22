// Package localopen holds the shared path-safety gate for "hand an
// AI-generated local path to the OS" flows. It exists so that both the
// desktop shell (OpenLocalPath / OpenLocalPathInExternalOpener) and builtin
// tools enforce the SAME gate: a prompt-injected
// or hallucinated path must never become an executable launch.
//
// The desktop package previously owned these functions; they moved here
// verbatim (task 944) so internal/tool/builtin can import them without
// importing package main. desktop/open_local_path.go keeps same-named thin
// wrappers so the opener family's call sites and tests are untouched.
package localopen

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// Normalize validates and normalizes a user-clicked local path before it is
// handed to the OS opener (or accepted as an artifact card target). It accepts
// either a plain absolute path (D:\a\b.md or D:/a/b.md) or a file URL. Callers
// on the native boundary validate independently because Wails methods are
// callable without the frontend.
func Normalize(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", os.ErrInvalid
	}
	if strings.HasPrefix(path, "file://") {
		parsed, err := url.Parse(path)
		if err != nil || parsed.Scheme != "file" || parsed.Opaque != "" || parsed.User != nil || parsed.Port() != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
			return "", fmt.Errorf("invalid local file URL %q", path)
		}
		decoded, err := url.PathUnescape(parsed.EscapedPath())
		if err != nil {
			return "", fmt.Errorf("invalid local file URL %q: %w", path, err)
		}
		host := parsed.Hostname()
		if host == "." || host == "?" {
			return "", fmt.Errorf("unsafe local file URL authority %q", host)
		}
		if strings.EqualFold(host, "localhost") {
			host = ""
		}
		if host != "" {
			decoded = "//" + host + decoded
		}
		if len(decoded) >= 4 && decoded[0] == '/' && isASCIILetter(decoded[1]) && decoded[2] == ':' && decoded[3] == '/' {
			decoded = decoded[1:]
		}
		path = decoded
	}
	if HasDisallowedWindowsPathSyntax(path) {
		return "", fmt.Errorf("unsafe local path syntax %q", path)
	}
	// Normalize forward slashes (file URLs, slash-form UNC "//nas/share")
	// to the platform-native separators the opener expects.
	path = filepath.FromSlash(path)
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("path is not absolute: %q", path)
	}
	return path, nil
}

func isASCIILetter(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z'
}

// HasDisallowedWindowsPathSyntax reports drive/UNC forms Windows would
// reinterpret (reserved device names, trailing-dot components, nested
// colons, \\?\ and \\.\ prefixes, NUL bytes).
func HasDisallowedWindowsPathSyntax(path string) bool {
	slashPath := strings.ReplaceAll(path, "\\", "/")
	if strings.ContainsRune(slashPath, '\x00') {
		return true
	}
	if slashPath == "//." || slashPath == "//?" || strings.HasPrefix(slashPath, "//./") || strings.HasPrefix(slashPath, "//?/") {
		return true
	}
	isDrivePath := len(slashPath) >= 3 && isASCIILetter(slashPath[0]) && slashPath[1] == ':' && slashPath[2] == '/'
	isUNCPath := strings.HasPrefix(slashPath, "//")
	if !isDrivePath && !isUNCPath {
		return false
	}
	remainder := slashPath[2:]
	if strings.Contains(remainder, ":") {
		return true
	}
	for component := range strings.SplitSeq(remainder, "/") {
		component = strings.TrimRight(component, " .")
		if dot := strings.IndexByte(component, '.'); dot >= 0 {
			component = component[:dot]
		}
		switch strings.ToUpper(component) {
		case "CON", "PRN", "AUX", "NUL", "CLOCK$", "CONIN$", "CONOUT$",
			"COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
			"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9",
			"COM¹", "COM²", "COM³", "LPT¹", "LPT²", "LPT³":
			return true
		}
	}
	return false
}

// TargetAllowed reports whether a resolved path may be handed to the OS
// "open" verb (or carded as an artifact). Directories and documents open
// normally; executable targets are refused because this gate is fed by
// AI-generated chat content — a prompt-injected or hallucinated ".bat" path
// must not run on click, and must not get a card with an open button either.
// openWorkspacePath remains outside this executable-target guard because its
// callers use it for paths already authorized by the workspace boundary.
var executableOpenSuffixes = map[string]bool{
	".app": true,
	".bat": true, ".cmd": true, ".com": true, ".exe": true,
	".desktop": true,
	".ps1":     true, ".vbs": true, ".jse": true, ".js": true,
	".lnk": true, ".url": true, ".scr": true, ".msi": true,
	".reg": true, ".pif": true, ".hta": true, ".wsf": true,
}

func TargetAllowed(path string, isDir bool, mode os.FileMode) bool {
	// Windows resolves trailing dots and spaces away before opening a path, and
	// filepath.Clean removes a trailing separator from macOS app bundles.
	base := strings.TrimRight(filepath.Base(filepath.Clean(path)), " .")
	if executableOpenSuffixes[strings.ToLower(filepath.Ext(base))] {
		return false
	}
	return isDir || mode.Perm()&0o111 == 0
}

// TargetPathAllowed is TargetAllowed plus the symlink check: a non-executable
// path whose symlink resolution lands on an executable target is refused.
func TargetPathAllowed(path string, info os.FileInfo) bool {
	if !TargetAllowed(path, info.IsDir(), info.Mode()) {
		return false
	}
	cleanPath := filepath.Clean(path)
	linkInfo, err := os.Lstat(cleanPath)
	if err != nil {
		return false
	}
	if linkInfo.Mode()&os.ModeSymlink == 0 {
		return true
	}
	resolved, err := filepath.EvalSymlinks(cleanPath)
	if err != nil {
		return false
	}
	return TargetAllowed(resolved, info.IsDir(), info.Mode())
}
