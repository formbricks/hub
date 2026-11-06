package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const (
	defaultEmbeddingReadinessFile = "/tmp/hub-embedding-ready"
	embeddingReadinessMode        = 0o600
)

func embeddingReadinessFile() string {
	if path := os.Getenv("HUB_EMBEDDING_READINESS_FILE"); path != "" {
		return path
	}

	return defaultEmbeddingReadinessFile
}

func setEmbeddingReadiness(ready bool) error {
	path := embeddingReadinessFile()
	if !ready {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove embedding readiness file: %w", err)
		}

		return nil
	}

	temporary, err := os.CreateTemp(filepath.Dir(path), ".embedding-ready-*")
	if err != nil {
		return fmt.Errorf("create embedding readiness file: %w", err)
	}
	defer func() { _ = os.Remove(temporary.Name()) }()

	if err := temporary.Chmod(embeddingReadinessMode); err != nil {
		_ = temporary.Close()

		return fmt.Errorf("protect embedding readiness file: %w", err)
	}

	if _, err := temporary.WriteString("ready\n"); err != nil {
		_ = temporary.Close()

		return fmt.Errorf("write embedding readiness file: %w", err)
	}

	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close embedding readiness file: %w", err)
	}

	if err := os.Rename(temporary.Name(), path); err != nil {
		return fmt.Errorf("replace embedding readiness file: %w", err)
	}

	return nil
}

func checkEmbeddingReadiness() bool {
	info, err := os.Stat(embeddingReadinessFile())

	return err == nil && info.Mode().IsRegular()
}
