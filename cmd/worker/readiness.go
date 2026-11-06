package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
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

func setEmbeddingReadiness(expiresAt time.Time) error {
	path := embeddingReadinessFile()
	if expiresAt.IsZero() || !time.Now().Before(expiresAt) {
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

	if _, err := temporary.WriteString(strconv.FormatInt(expiresAt.UnixNano(), 10) + "\n"); err != nil {
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
	data, err := os.ReadFile(embeddingReadinessFile())
	if err != nil {
		return false
	}

	expiresNano, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return false
	}

	return time.Now().Before(time.Unix(0, expiresNano))
}
