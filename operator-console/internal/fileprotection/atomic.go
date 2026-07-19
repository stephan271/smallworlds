package fileprotection

import (
	"fmt"
	"os"
	"path/filepath"
)

func WriteFileAtomically(path string, contents []byte) (returnErr error) {
	temporary, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create protected temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	committed := false
	defer func() {
		if !committed {
			_ = temporary.Close()
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := SecureFile(temporaryPath); err != nil {
		return err
	}
	if _, err := temporary.Write(contents); err != nil {
		return fmt.Errorf("write protected temporary file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync protected temporary file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close protected temporary file: %w", err)
	}
	if err := replaceFile(temporaryPath, path); err != nil {
		return fmt.Errorf("replace protected file: %w", err)
	}
	committed = true
	return SecureFile(path)
}
