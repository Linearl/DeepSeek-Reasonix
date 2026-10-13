//go:build darwin && !cgo

package main

// Without cgo there is no bridge to NSFileManager trashItemAtURL. Fail closed:
// the recycle-bin semantic must never degrade into an unrecoverable delete.
func trashPlatformPaths(paths []string) error {
	return errTrashUnsupported
}
