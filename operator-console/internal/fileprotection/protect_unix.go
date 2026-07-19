//go:build !windows

package fileprotection

import "os"

func SecureDirectory(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	return os.Chmod(path, 0o700)
}

func SecureFile(path string) error {
	return os.Chmod(path, 0o600)
}

func replaceFile(from, to string) error {
	return os.Rename(from, to)
}
