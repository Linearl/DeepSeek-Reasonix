//go:build !windows

package localdumps

// RootUser is meaningless off Windows.
var RootUser = Root(0)

// EnsureMachine has no WER facility behind it off Windows.
func EnsureMachine(imageName string, cfg Config) error {
	_, _ = imageName, cfg
	return ErrUnsupported
}

// ReadMachine has no WER facility behind it off Windows.
func ReadMachine(imageName string) (Config, error) {
	_ = imageName
	return Config{}, ErrUnsupported
}

// ReadUser has no WER facility behind it off Windows.
func ReadUser(imageName string) (Config, error) {
	_ = imageName
	return Config{}, ErrUnsupported
}

// DeleteMachine has no WER facility behind it off Windows.
func DeleteMachine(imageName string) error {
	_ = imageName
	return ErrUnsupported
}
