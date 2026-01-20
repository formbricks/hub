package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"

	"github.com/xernobyl/formbricks_worktrial/internal/config"
	"github.com/xernobyl/formbricks_worktrial/pkg/database"
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

	// Generate API key
	apiKey := "test-api-key-12345"

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

	var id string
	var name string
	var createdAt interface{}

	err = db.QueryRow(ctx, query, keyHash, "Test API Key", true).Scan(&id, &name, &createdAt)
	if err != nil {
		slog.Error("Failed to create/update API key", "error", err)
		os.Exit(1)
	}

	fmt.Println("✓ API key ready!")
	fmt.Println()
	fmt.Println("ID:", id)
	fmt.Println("Name:", name)
	fmt.Println("Created:", createdAt)
	fmt.Println()
	fmt.Println("API Key (use this in your requests):", apiKey)
	fmt.Println()
	fmt.Println("Example curl commands:")
	fmt.Println()
	fmt.Printf("# List all experiences\n")
	fmt.Printf("curl -H \"Authorization: Bearer %s\" http://localhost:8080/v1/experiences\n", apiKey)
	fmt.Println()
	fmt.Printf("# Create an experience\n")
	fmt.Printf("curl -X POST -H \"Authorization: Bearer %s\" -H \"Content-Type: application/json\" \\\n", apiKey)
	fmt.Printf("  -d '{\"source_type\":\"formbricks\",\"field_id\":\"feedback\",\"field_type\":\"text\",\"value_text\":\"Great product!\"}' \\\n")
	fmt.Printf("  http://localhost:8080/v1/experiences\n")
}
