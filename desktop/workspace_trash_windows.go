//go:build windows

package main

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

// FO_DELETE with FOF_ALLOWUNDO is the Windows Shell API for recycle-bin
// deletion (send to Recycle Bin, restorable), replacing a self-managed mv.
const (
	shFileOpDelete         uint32 = 3
	fofSilent              uint16 = 0x0004
	fofNoConfirmation      uint16 = 0x0010
	fofAllowUndo           uint16 = 0x0040
	fofNoErrorUI           uint16 = 0x0400
	fofNoConnectedElements uint16 = 0x2000
	fofWantNukeWarning     uint16 = 0x4000
)

type shFileOpStructW struct {
	hwnd                  windows.HWND
	wFunc                 uint32
	pFrom                 *uint16
	pTo                   *uint16
	fFlags                uint16
	fAnyOperationsAborted int32
	hNameMappings         uintptr
	lpszProgressTitle     *uint16
}

var shell32SHFileOperationW = windows.NewLazySystemDLL("shell32.dll").NewProc("SHFileOperationW")

func trashPlatformPaths(paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	if err := shell32SHFileOperationW.Find(); err != nil {
		return fmt.Errorf("shell32 SHFileOperationW unavailable: %w", err)
	}
	var builder strings.Builder
	for _, p := range paths {
		builder.WriteString(p)
		builder.WriteString("\x00")
	}
	builder.WriteString("\x00")
	// Manual UTF-16 encoding: UTF16PtrFromString rejects interior NULs, but the
	// double-NUL-terminated multi-path list is exactly what SHFileOperationW needs.
	encoded := utf16.Encode([]rune(builder.String()))
	if len(encoded) == 0 {
		return errors.New("no paths to trash")
	}
	op := shFileOpStructW{
		wFunc: shFileOpDelete,
		pFrom: &encoded[0],
		// FOF_ALLOWUNDO is the recycle-bin semantic; NOCONFIRMATION/SILENT/
		// NOERRORUI keep the panel in control of feedback; WANTNUKEWARNING
		// guards oversized entries the bin cannot hold.
		fFlags: fofAllowUndo | fofNoConfirmation | fofSilent | fofNoErrorUI | fofNoConnectedElements | fofWantNukeWarning,
	}
	ret, _, _ := shell32SHFileOperationW.Call(uintptr(unsafe.Pointer(&op)))
	if ret != 0 {
		return fmt.Errorf("recycle-bin delete failed (SHFileOperationW code %#x)", ret)
	}
	if op.fAnyOperationsAborted != 0 {
		return errors.New("recycle-bin delete was aborted")
	}
	return nil
}
