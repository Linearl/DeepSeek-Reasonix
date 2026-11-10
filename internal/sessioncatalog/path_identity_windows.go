//go:build windows

package sessioncatalog

import (
	"encoding/binary"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/windows"
)

func platformCatalogPathIdentity(path string) string {
	path = stripWindowsCatalogExtendedPrefix(path)
	return windowsCatalogPathIdentityBy(path, windowsDirectoryCaseInsensitiveCached)
}

// tl1526: the per-component case probe opens the directory with CreateFile on
// every identity resolution; a directory reconcile of a large store paid that
// per session record. A 30s per-directory memo keeps the semantics (a flipped
// case-sensitivity flag is picked up on the next TTL) at one probe per
// directory per TTL.
const windowsCaseCacheTTL = 30 * time.Second

type windowsCaseEntry struct {
	insensitive bool
	known       bool
	expiresAt   time.Time
}

var (
	windowsCaseMu    sync.Mutex
	windowsCaseCache = map[string]windowsCaseEntry{}
	// Test seam: tests swap this to count real probes.
	windowsCaseProbe = windowsCatalogDirectoryCaseInsensitive
)

func windowsDirectoryCaseInsensitiveCached(directory string) (bool, bool) {
	windowsCaseMu.Lock()
	if entry, ok := windowsCaseCache[directory]; ok && time.Now().Before(entry.expiresAt) {
		windowsCaseMu.Unlock()
		return entry.insensitive, entry.known
	}
	windowsCaseMu.Unlock()
	insensitive, known := windowsCaseProbe(directory)
	windowsCaseMu.Lock()
	windowsCaseCache[directory] = windowsCaseEntry{insensitive: insensitive, known: known, expiresAt: time.Now().Add(windowsCaseCacheTTL)}
	windowsCaseMu.Unlock()
	return insensitive, known
}

// windowsCatalogPathIdentityBy folds each component according to the directory
// that governs its lookup. Windows case sensitivity is per-directory, so
// lowercasing the whole string would merge distinct children of a WSL-enabled
// case-sensitive directory, while preserving the whole string would split
// drive-letter and insensitive-ancestor variants.
func windowsCatalogPathIdentityBy(path string, directoryCaseInsensitive func(string) (bool, bool)) string {
	volume := filepath.VolumeName(path)
	if volume == "" {
		return strings.ToLower(path)
	}
	current := volume + string(filepath.Separator)
	identity := strings.ToLower(current)
	caseInsensitive := true
	rest := strings.TrimLeft(path[len(volume):], `\/`)
	components := strings.FieldsFunc(rest, func(r rune) bool { return r == '\\' || r == '/' })
	for _, component := range components {
		if detected, ok := directoryCaseInsensitive(current); ok {
			caseInsensitive = detected
		}
		identityComponent := component
		if caseInsensitive {
			identityComponent = strings.ToLower(identityComponent)
		}
		identity = filepath.Join(identity, identityComponent)
		current = filepath.Join(current, component)
	}
	return filepath.Clean(identity)
}

func windowsCatalogDirectoryCaseInsensitive(directory string) (bool, bool) {
	name, err := windows.UTF16PtrFromString(directory)
	if err != nil {
		return true, false
	}
	handle, err := windows.CreateFile(
		name,
		windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS,
		0,
	)
	if err != nil {
		return true, false
	}
	defer windows.CloseHandle(handle)

	var info [4]byte
	err = windows.GetFileInformationByHandleEx(
		handle,
		windows.FileCaseSensitiveInfo,
		&info[0],
		uint32(len(info)),
	)
	if err == nil {
		return binary.LittleEndian.Uint32(info[:])&windows.FILE_CS_FLAG_CASE_SENSITIVE_DIR == 0, true
	}
	return true, false
}

func stripWindowsCatalogExtendedPrefix(path string) string {
	if strings.HasPrefix(strings.ToUpper(path), `\\?\UNC\`) {
		return `\\` + path[len(`\\?\UNC\`):]
	}
	return strings.TrimPrefix(path, `\\?\`)
}
