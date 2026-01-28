// Package main provides a CLI tool to ingest feedback from a Hub API format CSV file.
// This reads CSV files with columns matching the CreateFeedbackRecordRequest model.
//
// Usage:
//
//	go run scripts/ingest_hub_csv.go -file /path/to/feedback.csv -api-url http://localhost:8080 -api-key YOUR_API_KEY
package main

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds the CLI configuration
type Config struct {
	FilePath   string
	APIBaseURL string
	APIKey     string
	DelayMS    int
	DryRun     bool
}

// FeedbackRequest matches the CreateFeedbackRecordRequest model
type FeedbackRequest struct {
	CollectedAt    *string         `json:"collected_at,omitempty"`
	SourceType     string          `json:"source_type"`
	SourceID       *string         `json:"source_id,omitempty"`
	SourceName     *string         `json:"source_name,omitempty"`
	FieldID        string          `json:"field_id"`
	FieldLabel     *string         `json:"field_label,omitempty"`
	FieldType      string          `json:"field_type"`
	ValueText      *string         `json:"value_text,omitempty"`
	ValueNumber    *float64        `json:"value_number,omitempty"`
	ValueBoolean   *bool           `json:"value_boolean,omitempty"`
	ValueDate      *string         `json:"value_date,omitempty"`
	Metadata       json.RawMessage `json:"metadata,omitempty"`
	Language       *string         `json:"language,omitempty"`
	UserIdentifier *string         `json:"user_identifier,omitempty"`
	TenantID       *string         `json:"tenant_id,omitempty"`
	ResponseID     *string         `json:"response_id,omitempty"`
}

// Stats tracks ingestion statistics
type Stats struct {
	TotalRows       int
	SkippedEmpty    int
	SuccessfulPosts int
	FailedPosts     int
}

// CSV column indices for Hub API format (0-based)
const (
	colCollectedAt    = 0
	colFieldID        = 1
	colFieldLabel     = 2
	colFieldType      = 3
	colLanguage       = 4
	colMetadata       = 5
	colResponseID     = 6
	colSourceID       = 7
	colSourceName     = 8
	colSourceType     = 9
	colTenantID       = 10
	colUserIdentifier = 11
	colValueBoolean   = 12
	colValueDate      = 13
	colValueNumber    = 14
	colValueText      = 15
)

func main() {
	cfg := parseFlags()

	if cfg.FilePath == "" {
		fmt.Println("Error: -file is required")
		flag.Usage()
		os.Exit(1)
	}

	if cfg.APIKey == "" {
		fmt.Println("Error: -api-key is required")
		flag.Usage()
		os.Exit(1)
	}

	fmt.Printf("🚀 Formbricks Hub CSV Ingestion Tool (Hub Format)\n")
	fmt.Printf("   API URL: %s\n", cfg.APIBaseURL)
	fmt.Printf("   CSV File: %s\n", cfg.FilePath)
	fmt.Printf("   Delay: %dms between requests\n", cfg.DelayMS)
	if cfg.DryRun {
		fmt.Printf("   ⚠️  DRY RUN MODE - No actual API calls will be made\n")
	}
	fmt.Println()

	// Process CSV
	stats := processCSV(cfg)

	// Print summary
	fmt.Println()
	fmt.Println("📊 Ingestion Summary")
	fmt.Println("   ─────────────────────")
	fmt.Printf("   Total rows processed:  %d\n", stats.TotalRows)
	fmt.Printf("   Skipped (empty/invalid): %d\n", stats.SkippedEmpty)
	fmt.Printf("   Successfully created:  %d\n", stats.SuccessfulPosts)
	fmt.Printf("   Failed:                %d\n", stats.FailedPosts)
	fmt.Println()

	if stats.FailedPosts > 0 {
		os.Exit(1)
	}
}

func parseFlags() Config {
	cfg := Config{}

	flag.StringVar(&cfg.FilePath, "file", "", "Path to CSV file in Hub API format (required)")
	flag.StringVar(&cfg.APIBaseURL, "api-url", "http://localhost:8080", "Hub API base URL")
	flag.StringVar(&cfg.APIKey, "api-key", "", "API key for authentication (required)")
	flag.IntVar(&cfg.DelayMS, "delay", 100, "Delay in milliseconds between API calls")
	flag.BoolVar(&cfg.DryRun, "dry-run", false, "Parse CSV but don't make API calls")

	flag.Parse()
	return cfg
}

func processCSV(cfg Config) Stats {
	stats := Stats{}

	file, err := os.Open(cfg.FilePath)
	if err != nil {
		fmt.Printf("Error opening file: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = file.Close() }()

	reader := csv.NewReader(file)
	reader.FieldsPerRecord = -1 // Allow variable field counts
	reader.LazyQuotes = true    // Handle quotes more leniently

	client := &http.Client{Timeout: 30 * time.Second}

	// Read and validate header row
	header, err := reader.Read()
	if err != nil {
		fmt.Printf("Error reading header: %v\n", err)
		os.Exit(1)
	}

	// Validate header matches expected format
	expectedHeader := []string{
		"collected_at", "field_id", "field_label", "field_type", "language",
		"metadata", "response_id", "source_id", "source_name", "source_type",
		"tenant_id", "user_identifier", "value_boolean", "value_date",
		"value_number", "value_text",
	}

	if len(header) < len(expectedHeader) {
		fmt.Printf("Error: CSV has %d columns, expected at least %d\n", len(header), len(expectedHeader))
		fmt.Printf("Expected columns: %v\n", expectedHeader)
		os.Exit(1)
	}

	fmt.Println("📥 Ingesting feedback records...")
	fmt.Println()

	rowNum := 1
	for {
		row, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			fmt.Printf("   ⚠ Row %d: Error reading: %v\n", rowNum, err)
			stats.SkippedEmpty++
			rowNum++
			continue
		}

		stats.TotalRows++
		feedback, err := extractFeedbackFromRow(row)
		if err != nil {
			if cfg.DryRun {
				fmt.Printf("   [SKIP] Row %d: %v\n", rowNum, err)
			}
			stats.SkippedEmpty++
			rowNum++
			continue
		}

		if cfg.DryRun {
			fmt.Printf("   [DRY] Row %d: %s (%s) - %s\n", rowNum, feedback.FieldID, feedback.FieldType, truncate(getValuePreview(feedback), 50))
			stats.SuccessfulPosts++
			rowNum++
			continue
		}

		err = postFeedback(client, cfg, feedback)
		if err != nil {
			fmt.Printf("   ✗ Row %d (%s): %v\n", rowNum, feedback.FieldID, err)
			stats.FailedPosts++
		} else {
			fmt.Printf("   ✓ Row %d: %s (%s)\n", rowNum, feedback.FieldID, feedback.FieldType)
			stats.SuccessfulPosts++
		}

		time.Sleep(time.Duration(cfg.DelayMS) * time.Millisecond)
		rowNum++
	}

	return stats
}

func extractFeedbackFromRow(row []string) (*FeedbackRequest, error) {
	// Check required fields
	fieldID := strings.TrimSpace(safeGet(row, colFieldID))
	fieldType := strings.TrimSpace(safeGet(row, colFieldType))
	sourceType := strings.TrimSpace(safeGet(row, colSourceType))

	if fieldID == "" {
		return nil, fmt.Errorf("missing required field: field_id")
	}
	if fieldType == "" {
		return nil, fmt.Errorf("missing required field: field_type")
	}
	if sourceType == "" {
		return nil, fmt.Errorf("missing required field: source_type")
	}

	feedback := &FeedbackRequest{
		FieldID:    fieldID,
		FieldType:  fieldType,
		SourceType: sourceType,
	}

	// Parse collected_at timestamp
	if ts := strings.TrimSpace(safeGet(row, colCollectedAt)); ts != "" {
		// Try parsing "2006-01-02 15:04:05" format
		if t, err := time.Parse("2006-01-02 15:04:05", ts); err == nil {
			formatted := t.Format(time.RFC3339)
			feedback.CollectedAt = &formatted
		} else if t, err := time.Parse(time.RFC3339, ts); err == nil {
			// Already in RFC3339 format
			formatted := t.Format(time.RFC3339)
			feedback.CollectedAt = &formatted
		}
	}

	// String fields
	feedback.FieldLabel = nilIfEmpty(strings.TrimSpace(safeGet(row, colFieldLabel)))
	feedback.Language = nilIfEmpty(strings.TrimSpace(safeGet(row, colLanguage)))
	feedback.ResponseID = nilIfEmpty(strings.TrimSpace(safeGet(row, colResponseID)))
	feedback.SourceID = nilIfEmpty(strings.TrimSpace(safeGet(row, colSourceID)))
	feedback.SourceName = nilIfEmpty(strings.TrimSpace(safeGet(row, colSourceName)))
	feedback.TenantID = nilIfEmpty(strings.TrimSpace(safeGet(row, colTenantID)))
	feedback.UserIdentifier = nilIfEmpty(strings.TrimSpace(safeGet(row, colUserIdentifier)))
	feedback.ValueText = nilIfEmpty(strings.TrimSpace(safeGet(row, colValueText)))
	feedback.ValueDate = nilIfEmpty(strings.TrimSpace(safeGet(row, colValueDate)))

	// Parse metadata JSON
	if metaStr := strings.TrimSpace(safeGet(row, colMetadata)); metaStr != "" {
		// Validate it's valid JSON
		var js json.RawMessage
		if err := json.Unmarshal([]byte(metaStr), &js); err == nil {
			feedback.Metadata = js
		}
	}

	// Parse value_number
	if numStr := strings.TrimSpace(safeGet(row, colValueNumber)); numStr != "" {
		if num, err := strconv.ParseFloat(numStr, 64); err == nil {
			feedback.ValueNumber = &num
		}
	}

	// Parse value_boolean
	if boolStr := strings.TrimSpace(safeGet(row, colValueBoolean)); boolStr != "" {
		boolStr = strings.ToLower(boolStr)
		switch boolStr {
		case "true", "1", "yes":
			b := true
			feedback.ValueBoolean = &b
		case "false", "0", "no":
			b := false
			feedback.ValueBoolean = &b
		}
	}

	return feedback, nil
}

func postFeedback(client *http.Client, cfg Config, feedback *FeedbackRequest) error {
	body, err := json.Marshal(feedback)
	if err != nil {
		return fmt.Errorf("marshal error: %w", err)
	}

	req, err := http.NewRequest("POST", cfg.APIBaseURL+"/v1/feedback-records", bytes.NewReader(body))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("status %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

func safeGet(row []string, index int) string {
	if index >= 0 && index < len(row) {
		return row[index]
	}
	return ""
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

func getValuePreview(f *FeedbackRequest) string {
	if f.ValueText != nil && *f.ValueText != "" {
		return *f.ValueText
	}
	if f.ValueNumber != nil {
		return fmt.Sprintf("%v", *f.ValueNumber)
	}
	if f.ValueBoolean != nil {
		return fmt.Sprintf("%v", *f.ValueBoolean)
	}
	if f.ValueDate != nil && *f.ValueDate != "" {
		return *f.ValueDate
	}
	return "(no value)"
}
