//go:build !windows

package localdumps

// Root is the hive handle type; off Windows there is no registry, so it is a
// meaningless int that only exists to keep the API shape identical.
type Root = int

// Ensure has no WER facility behind it off Windows.
func Ensure(root Root, imageName string, cfg Config) error {
	return ErrUnsupported
}

// Read has no WER facility behind it off Windows.
func Read(root Root, imageName string) (Config, error) {
	return Config{}, ErrUnsupported
}

// Delete has no WER facility behind it off Windows.
func Delete(root Root, imageName string) error {
	return ErrUnsupported
}

// DefaultDumpFolder has no WER default off Windows.
func DefaultDumpFolder() (string, error) {
	return "", ErrUnsupported
}

// IsAccessDenied is always false off Windows.
func IsAccessDenied(err error) bool {
	_ = err
	return false
}
