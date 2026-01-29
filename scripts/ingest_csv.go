// Package main provides a CLI tool to ingest feedback from a CSV file into the Hub API.
// This simulates real production usage by making API calls with proper authentication.
//
// Usage:
//
//	go run scripts/ingest_csv.go -file /path/to/feedback.csv -api-url http://localhost:8080 -api-key YOUR_API_KEY
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
	FilePath     string
	APIBaseURL   string
	APIKey       string
	CreateTopics bool
	DelayMS      int
	DryRun       bool
	TenantID     string
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
	Metadata       json.RawMessage `json:"metadata,omitempty"`
	Language       *string         `json:"language,omitempty"`
	UserIdentifier *string         `json:"user_identifier,omitempty"`
	TenantID       *string         `json:"tenant_id,omitempty"`
	ResponseID     *string         `json:"response_id,omitempty"`
}

// TopicRequest matches the CreateTopicRequest model
type TopicRequest struct {
	Title    string  `json:"title"`
	Level    int     `json:"level"`
	ParentID *string `json:"parent_id,omitempty"`
	TenantID *string `json:"tenant_id,omitempty"`
}

// APIResponse represents a generic API response
type APIResponse struct {
	ID    string `json:"id"`
	Error string `json:"error,omitempty"`
}

// Stats tracks ingestion statistics
type Stats struct {
	TotalRows       int
	SkippedEmpty    int
	SuccessfulPosts int
	FailedPosts     int
	TopicsCreated   int
}

// Default topics to seed for classification
var defaultTopics = []struct {
	Title    string
	Children []string
}{
	{
		Title:    "Performance",
		Children: []string{"Slow Loading", "Dashboard Performance", "API Response Time"},
	},
	{
		Title:    "User Experience",
		Children: []string{"Survey Results Viewing", "Navigation", "Mobile Experience"},
	},
	{
		Title:    "Feature Requests",
		Children: []string{"Custom Dashboards", "Import/Export", "Workflows", "AI Features"},
	},
	{
		Title:    "Integrations",
		Children: []string{"Third-party Apps", "API Access", "Webhooks"},
	},
	{
		Title:    "Authentication",
		Children: []string{"Login Issues", "Session Management", "SSO"},
	},
	{
		Title:    "Pricing",
		Children: []string{"Feature Deprecation", "Plan Limitations", "Value for Money"},
	},
}

// CSV column indices for normalized format (0-based)
// Format: collected_at;field_id;field_label;field_type;language;metadata;response_id;source_id;source_name;source_type;tenant_id;user_identifier;value_boolean;value_date;value_number;value_text
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

	fmt.Printf("🚀 Formbricks Hub CSV Ingestion Tool\n")
	fmt.Printf("   API URL: %s\n", cfg.APIBaseURL)
	fmt.Printf("   CSV File: %s\n", cfg.FilePath)
	fmt.Printf("   Delay: %dms between requests\n", cfg.DelayMS)
	if cfg.DryRun {
		fmt.Printf("   ⚠️  DRY RUN MODE - No actual API calls will be made\n")
	}
	fmt.Println()

	// Create topics first if requested
	if cfg.CreateTopics && !cfg.DryRun {
		fmt.Println("📂 Creating topics for classification...")
		topicsCreated := createTopics(cfg)
		fmt.Printf("   ✓ Created %d topics\n\n", topicsCreated)
	}

	// Process CSV
	stats := processCSV(cfg)

	// Print summary
	fmt.Println()
	fmt.Println("📊 Ingestion Summary")
	fmt.Println("   ─────────────────────")
	fmt.Printf("   Total rows processed:  %d\n", stats.TotalRows)
	fmt.Printf("   Skipped (empty):       %d\n", stats.SkippedEmpty)
	fmt.Printf("   Successfully created:  %d\n", stats.SuccessfulPosts)
	fmt.Printf("   Failed:                %d\n", stats.FailedPosts)
	if cfg.CreateTopics {
		fmt.Printf("   Topics created:        %d\n", stats.TopicsCreated)
	}
	fmt.Println()

	if stats.FailedPosts > 0 {
		os.Exit(1)
	}
}

func parseFlags() Config {
	cfg := Config{}

	flag.StringVar(&cfg.FilePath, "file", "", "Path to CSV file (required)")
	flag.StringVar(&cfg.APIBaseURL, "api-url", "http://localhost:8080", "Hub API base URL")
	flag.StringVar(&cfg.APIKey, "api-key", "", "API key for authentication (required)")
	flag.BoolVar(&cfg.CreateTopics, "create-topics", true, "Create default topics before ingesting")
	flag.IntVar(&cfg.DelayMS, "delay", 100, "Delay in milliseconds between API calls")
	flag.BoolVar(&cfg.DryRun, "dry-run", false, "Parse CSV but don't make API calls")
	flag.StringVar(&cfg.TenantID, "tenant-id", "", "Optional tenant ID for all records")

	flag.Parse()
	return cfg
}

func createTopics(cfg Config) int {
	count := 0
	client := &http.Client{Timeout: 10 * time.Second}

	for _, level1Topic := range defaultTopics {
		// Create Level 1 topic
		level1ID, err := createTopic(client, cfg, level1Topic.Title, 1, nil)
		if err != nil {
			fmt.Printf("   ⚠ Failed to create Level 1 topic '%s': %v\n", level1Topic.Title, err)
			continue
		}
		count++
		fmt.Printf("   + %s (id: %s)\n", level1Topic.Title, level1ID)

		// Create Level 2 topics with parent_id
		for _, level2Title := range level1Topic.Children {
			_, err := createTopic(client, cfg, level2Title, 2, &level1ID)
			if err != nil {
				fmt.Printf("   ⚠ Failed to create Level 2 topic '%s': %v\n", level2Title, err)
				continue
			}
			count++
			fmt.Printf("     └─ %s\n", level2Title)
		}

		time.Sleep(time.Duration(cfg.DelayMS) * time.Millisecond)
	}

	return count
}

func createTopic(client *http.Client, cfg Config, title string, level int, parentID *string) (string, error) {
	req := TopicRequest{
		Title:    title,
		Level:    level,
		ParentID: parentID,
	}
	if cfg.TenantID != "" {
		req.TenantID = &cfg.TenantID
	}

	body, _ := json.Marshal(req)
	httpReq, err := http.NewRequest("POST", cfg.APIBaseURL+"/v1/topics", bytes.NewReader(body))
	if err != nil {
		return "", err
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+cfg.APIKey)

	resp, err := client.Do(httpReq)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("status %d: %s", resp.StatusCode, string(respBody))
	}

	var apiResp APIResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return "", err
	}

	return apiResp.ID, nil
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
	reader.Comma = ';'          // Use semicolon as delimiter
	reader.FieldsPerRecord = -1 // Allow variable field counts
	reader.LazyQuotes = true    // Handle quotes more leniently

	client := &http.Client{Timeout: 10 * time.Second}

	// Skip header row
	_, err = reader.Read()
	if err != nil {
		fmt.Printf("Error reading header: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("📥 Ingesting feedback records...")

	rowNum := 1
	for {
		row, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			fmt.Printf("   ⚠ Row %d: Error reading: %v\n", rowNum, err)
			rowNum++
			continue
		}

		stats.TotalRows++
		feedbackRecords := extractFeedbackFromRow(row, cfg)

		if len(feedbackRecords) == 0 {
			stats.SkippedEmpty++
			rowNum++
			continue
		}

		for _, feedback := range feedbackRecords {
			if cfg.DryRun {
				fmt.Printf("   [DRY] Row %d: Would create %s feedback\n", rowNum, feedback.FieldID)
				stats.SuccessfulPosts++
				continue
			}

			err := postFeedback(client, cfg, feedback)
			if err != nil {
				fmt.Printf("   ✗ Row %d (%s): %v\n", rowNum, feedback.FieldID, err)
				stats.FailedPosts++
			} else {
				fmt.Printf("   ✓ Row %d: %s\n", rowNum, feedback.FieldID)
				stats.SuccessfulPosts++
			}

			time.Sleep(time.Duration(cfg.DelayMS) * time.Millisecond)
		}

		rowNum++
	}

	return stats
}

func extractFeedbackFromRow(row []string, cfg Config) []FeedbackRequest {
	// Normalized CSV format: each row is one feedback record
	// Format: collected_at;field_id;field_label;field_type;language;metadata;response_id;source_id;source_name;source_type;tenant_id;user_identifier;value_boolean;value_date;value_number;value_text

	// Get required fields
	fieldID := strings.TrimSpace(safeGet(row, colFieldID))
	fieldType := strings.TrimSpace(safeGet(row, colFieldType))
	sourceType := strings.TrimSpace(safeGet(row, colSourceType))
	valueText := strings.TrimSpace(safeGet(row, colValueText))

	// Skip if no text content (we only process text feedback for taxonomy)
	if valueText == "" {
		return nil
	}

	// Skip if missing required fields
	if fieldID == "" || fieldType == "" || sourceType == "" {
		return nil
	}

	// Get optional fields
	collectedAtRaw := strings.TrimSpace(safeGet(row, colCollectedAt))
	fieldLabel := strings.TrimSpace(safeGet(row, colFieldLabel))
	language := strings.TrimSpace(safeGet(row, colLanguage))
	metadataRaw := strings.TrimSpace(safeGet(row, colMetadata))
	responseID := strings.TrimSpace(safeGet(row, colResponseID))
	sourceID := strings.TrimSpace(safeGet(row, colSourceID))
	sourceName := strings.TrimSpace(safeGet(row, colSourceName))
	tenantID := strings.TrimSpace(safeGet(row, colTenantID))
	userIdentifier := strings.TrimSpace(safeGet(row, colUserIdentifier))
	valueNumberRaw := strings.TrimSpace(safeGet(row, colValueNumber))

	// Use tenant_id from CSV if not overridden by CLI
	if cfg.TenantID != "" {
		tenantID = cfg.TenantID
	}

	// Parse timestamp for collected_at
	var collectedAt *string
	if collectedAtRaw != "" {
		// Parse "2026-01-23 07:08:21" format and convert to RFC3339
		if t, err := time.Parse("2006-01-02 15:04:05", collectedAtRaw); err == nil {
			formatted := t.Format(time.RFC3339)
			collectedAt = &formatted
		} else {
			// Try RFC3339 format directly
			if _, err := time.Parse(time.RFC3339, collectedAtRaw); err == nil {
				collectedAt = &collectedAtRaw
			}
		}
	}

	// Parse metadata JSON
	var metadata json.RawMessage
	if metadataRaw != "" {
		// Validate it's valid JSON
		if json.Valid([]byte(metadataRaw)) {
			metadata = json.RawMessage(metadataRaw)
		}
	}

	// Parse value_number if present
	var valueNumber *float64
	if valueNumberRaw != "" {
		if num, err := strconv.ParseFloat(valueNumberRaw, 64); err == nil {
			valueNumber = &num
		}
	}

	req := FeedbackRequest{
		CollectedAt:    collectedAt,
		SourceType:     sourceType,
		SourceID:       nilIfEmpty(sourceID),
		SourceName:     nilIfEmpty(sourceName),
		FieldID:        fieldID,
		FieldLabel:     nilIfEmpty(fieldLabel),
		FieldType:      fieldType,
		ValueText:      nilIfEmpty(valueText),
		ValueNumber:    valueNumber,
		Metadata:       metadata,
		Language:       nilIfEmpty(language),
		UserIdentifier: nilIfEmpty(userIdentifier),
		TenantID:       nilIfEmpty(tenantID),
		ResponseID:     nilIfEmpty(responseID),
	}

	return []FeedbackRequest{req}
}

func postFeedback(client *http.Client, cfg Config, feedback FeedbackRequest) error {
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
