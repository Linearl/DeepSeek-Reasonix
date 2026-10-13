//go:build darwin && cgo

package main

/*
#cgo LDFLAGS: -framework Foundation
#include <stdlib.h>

int reasonix_trash_path(const char *path, char **error_message);
*/
import "C"

import (
	"errors"
	"fmt"
	"strings"
	"unsafe"
)

// trashPlatformPaths moves every path to the macOS Trash via
// NSFileManager trashItemAtURL — one call per path, failures aggregated.
func trashPlatformPaths(paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	var failures []string
	for _, p := range paths {
		if err := darwinTrashOne(p); err != nil {
			failures = append(failures, fmt.Sprintf("%s: %s", p, err.Error()))
		}
	}
	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "; "))
	}
	return nil
}

func darwinTrashOne(path string) error {
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))
	var cerr *C.char
	if code := C.reasonix_trash_path(cpath, &cerr); code != 0 {
		defer func() {
			if cerr != nil {
				C.free(unsafe.Pointer(cerr))
			}
		}()
		if cerr != nil {
			return fmt.Errorf("%s", C.GoString(cerr))
		}
		return errors.New("trashItemAtURL failed")
	}
	return nil
}
