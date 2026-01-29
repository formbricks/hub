# Hub Scripts

Utility scripts for development and testing.

## ingest_csv.go

Ingests feedback from a CSV file into the Hub API, simulating real production usage.

### Features

- **Creates default topics** for classification testing (Performance, UX, Features, etc.)
- **Extracts multiple feedback fields** from each CSV row
- **Sends via API** with proper authentication
- **Configurable delay** between requests to simulate realistic load
- **Dry-run mode** to preview without making API calls

### Usage

```bash
# Basic usage (with sample data)
go run scripts/ingest_csv.go \
  -file testdata/sample_feedback.csv \
  -api-key YOUR_API_KEY

# All options
go run scripts/ingest_csv.go \
  -file /path/to/feedback.csv \
  -api-url http://localhost:8080 \
  -api-key YOUR_API_KEY \
  -create-topics=true \
  -delay 100 \
  -tenant-id optional-tenant \
  -dry-run
```

### Options

| Flag | Default | Description |
|------|---------|-------------|
| `-file` | (required) | Path to the CSV file |
| `-api-url` | `http://localhost:8080` | Hub API base URL |
| `-api-key` | (required) | API key for authentication (uses `Authorization: Bearer` header) |
| `-create-topics` | `true` | Create default topics before ingesting |
| `-delay` | `100` | Milliseconds between API calls |
| `-tenant-id` | (empty) | Optional tenant ID for all records |
| `-dry-run` | `false` | Parse CSV but don't make API calls |

### CSV Format

The script expects a Formbricks survey export CSV with columns:
- Response ID (column 2)
- Timestamp (column 3)
- Country (column 12)
- Text feedback fields (columns 17-19, 30)
- NPS score (column 29)
- Email (column 32)

### Extracted Fields

For each CSV row, the script creates feedback records for:
1. `helped_solve` - How Formbricks helped solve problems
2. `help_better` - Suggestions for improvement
3. `missing_feature` - Missing feature requests
4. `nps_reason` - NPS score explanation
5. `nps_score` - Numeric NPS value (1-10)

### Example Output

```
🚀 Formbricks Hub CSV Ingestion Tool
   API URL: http://localhost:8080
   CSV File: /path/to/feedback.csv
   Delay: 100ms between requests

📂 Creating topics for classification...
   + Performance
     └─ Slow Loading
     └─ Dashboard Performance
     └─ API Response Time
   ...
   ✓ Created 24 topics

📥 Ingesting feedback records...
   ✓ Row 1: helped_solve
   ✓ Row 2: help_better
   ✓ Row 2: missing_feature
   ...

📊 Ingestion Summary
   ─────────────────────
   Total rows processed:  19
   Skipped (empty):       3
   Successfully created:  42
   Failed:                0
   Topics created:        24
```
