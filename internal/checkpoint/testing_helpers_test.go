package checkpoint

import (
	"os"
)

// Small filesystem helpers shared by the tl1476 safety tests.

func writeFileForTest(path string, data []byte) error {
	return os.WriteFile(path, data, 0o644)
}

func chmodReadOnlyForTest(path string) error {
	return os.Chmod(path, 0o444)
}

func mkdirReadOnlyForTest(path string) error {
	if err := os.Mkdir(path, 0o755); err != nil {
		return err
	}
	return os.Chmod(path, 0o555)
}
