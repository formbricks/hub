# Test Data

Sample data files for testing the Hub API.

## sample_feedback.csv

Export from the Formbricks Enterprise Dream Survey containing real feedback responses.

**Fields extracted by the ingestion script:**
- How Formbricks helped solve problems
- Suggestions for improvement  
- Missing feature requests
- NPS scores and reasons

**Usage:**

```bash
go run scripts/ingest_csv.go \
  -file testdata/sample_feedback.csv \
  -api-key YOUR_API_KEY
```
