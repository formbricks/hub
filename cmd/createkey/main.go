package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/formbricks/hub/internal/config"
	"github.com/formbricks/hub/pkg/database"
	"github.com/google/uuid"
)

func main() {
	ctx := context.Background()

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		slog.Error("Failed to load configuration", "error", err)
		os.Exit(1)
	}

	// Initialize database connection
	db, err := database.NewPostgresPool(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("Failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	// Generate a random API key
	// Character set: uppercase letters, lowercase letters, and numbers
	const charset = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	const keyLength = 32

	// Calculate charset length and rejection sampling threshold
	charsetLen := len(charset)
	// Use rejection sampling to avoid modulo bias
	// Calculate the largest multiple of charsetLen that fits in a byte (0-255)
	// This ensures uniform distribution across all characters
	maxValidByte := byte((255 / charsetLen) * charsetLen)

	// Build the API key by selecting random characters from the charset
	apiKeyBytes := make([]byte, keyLength)
	randomByte := make([]byte, 1)
	for i := range apiKeyBytes {
		// Use rejection sampling: keep generating until we get a value < maxValidByte
		for {
			if _, err := rand.Read(randomByte); err != nil {
				slog.Error("Failed to generate random API key", "error", err)
				os.Exit(1)
			}
			if randomByte[0] < maxValidByte {
				apiKeyBytes[i] = charset[int(randomByte[0])%charsetLen]
				break
			}
		}
	}

	apiKey := string(apiKeyBytes)

	// Hash the API key
	hash := sha256.Sum256([]byte(apiKey))
	keyHash := hex.EncodeToString(hash[:])

	// Try to insert, if it already exists, just show the info
	query := `
		INSERT INTO api_keys (key_hash, name, is_active)
		VALUES ($1, $2, $3)
		ON CONFLICT (key_hash) DO UPDATE SET is_active = true
		RETURNING id, name, created_at
	`

	var id uuid.UUID
	var name *string
	var createdAt time.Time

	err = db.QueryRow(ctx, query, keyHash, "Generated API Key", true).Scan(&id, &name, &createdAt)
	if err != nil {
		slog.Error("Failed to create/update API key", "error", err)
		os.Exit(1)
	}

	fmt.Println("✓ API key ready!")
	fmt.Println()
	fmt.Println("ID:", id)
	if name != nil {
		fmt.Println("Name:", *name)
	} else {
		fmt.Println("Name: (none)")
	}
	fmt.Println("Created:", createdAt)
	fmt.Println()
	fmt.Println("API Key (use this in your requests):", apiKey)
	fmt.Println()
	fmt.Println("Example curl commands:")
	fmt.Println()
	fmt.Printf("# List all feedback records\n")
	fmt.Printf("curl -H \"Authorization: Bearer %s\" http://localhost:8080/v1/feedback-records\n", apiKey)
	fmt.Println()
	fmt.Printf("# Create a feedback record\n")
	fmt.Printf("curl -X POST -H \"Authorization: Bearer %s\" -H \"Content-Type: application/json\" \\\n", apiKey)
	fmt.Printf("  -d '{\"source_type\":\"formbricks\",\"field_id\":\"feedback\",\"field_type\":\"text\",\"value_text\":\"Great product!\"}' \\\n")
	fmt.Printf("  http://localhost:8080/v1/feedback-records\n")
}
