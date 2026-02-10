#!/usr/bin/env bash
# Seed feedback_records with dummy data via the Hub API.
# Requires: curl, HUB_URL and API_KEY in environment (or source .env from repo root).
# Usage: ./scripts/seed-feedback-records.sh [count]
# Default count is 50. Example: HUB_URL=http://localhost:8080 API_KEY=your-key ./scripts/seed-feedback-records.sh 20

set -e

if ! command -v jq >/dev/null 2>&1; then
  echo "Error: jq is required. Install with: brew install jq (macOS) or apt-get install jq (Linux)"
  exit 1
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
if [ -f "$REPO_ROOT/.env" ]; then
  set -a
  # shellcheck source=/dev/null
  source "$REPO_ROOT/.env"
  set +a
fi

HUB_URL="${HUB_URL:-http://localhost:8080}"
API_KEY="${API_KEY:-}"

if [ -z "$API_KEY" ]; then
  echo "Error: API_KEY is required. Set it in the environment or in $REPO_ROOT/.env"
  exit 1
fi

COUNT="${1:-50}"
BASE_URL="$HUB_URL/v1/feedback-records"
AUTH_HEADER="Authorization: Bearer $API_KEY"

post() {
  curl -s -S -X POST "$BASE_URL" \
    -H "Content-Type: application/json" \
    -H "$AUTH_HEADER" \
    -d "$1"
}

# Dummy values for variety
SOURCES=("survey" "nps_campaign" "feedback_form" "support" "review")
SOURCE_IDS=("nps-q1-2025" "onboarding-survey" "csat-widget" "support-tickets" "app-store")
FIELD_TYPES_NUM=(nps csat ces rating number)
FIELD_TYPES_OTHER=(text categorical boolean)
TEXTS=(
  "Great product, would recommend."
  "The dashboard could be clearer."
  "Fast support, issue resolved quickly."
  "Could use more integrations."
  "Love the new UI."
)
CATEGORICALS=(Desktop Mobile Tablet Web)
USER_PREFIX="user-"
TENANT="${TENANT_ID:-}"

created=0
failed=0

for i in $(seq 1 "$COUNT"); do
  src_idx=$((i % ${#SOURCES[@]}))
  source_type="${SOURCES[$src_idx]}"
  source_id="${SOURCE_IDS[$src_idx]}"
  user_id="${USER_PREFIX}$(printf "%03d" $((i % 100)))"
  # Spread collected_at over last 90 days (portable: macOS BSD date vs GNU date)
  days_ago=$((i % 90))
  collected_at=$(date -u -v"-${days_ago}d" +"%Y-%m-%dT%H:%M:%SZ" 2>/dev/null) \
    || collected_at=$(date -u -d "-${days_ago} days" +"%Y-%m-%dT%H:%M:%SZ" 2>/dev/null) \
    || collected_at=$(date -u +"%Y-%m-%dT%H:%M:%SZ")

  case $((i % 8)) in
    0)
      # NPS (0-10)
      score=$((i % 11))
      body=$(jq -n \
        --arg st "$source_type" --arg sid "$source_id" --arg uid "$user_id" --arg at "$collected_at" \
        --argjson n "$score" \
        '{source_type: $st, source_id: $sid, source_name: "Dummy NPS Survey", field_id: "nps_score", field_label: "How likely are you to recommend us?", field_type: "nps", value_number: $n, user_identifier: $uid, collected_at: $at}')
      ;;
    1)
      # CSAT (1-5)
      score=$((i % 5 + 1))
      body=$(jq -n \
        --arg st "$source_type" --arg sid "$source_id" --arg uid "$user_id" --arg at "$collected_at" \
        --argjson n "$score" \
        '{source_type: $st, source_id: $sid, field_id: "satisfaction", field_label: "How satisfied are you?", field_type: "csat", value_number: $n, user_identifier: $uid, collected_at: $at}')
      ;;
    2)
      # Text feedback
      text="${TEXTS[$((i % ${#TEXTS[@]}))]}"
      body=$(jq -n \
        --arg st "$source_type" --arg sid "$source_id" --arg uid "$user_id" --arg at "$collected_at" --arg t "$text" \
        '{source_type: $st, source_id: $sid, field_id: "feedback_comment", field_label: "Any comments?", field_type: "text", value_text: $t, user_identifier: $uid, collected_at: $at}')
      ;;
    3)
      # Categorical
      cat_val="${CATEGORICALS[$((i % ${#CATEGORICALS[@]}))]}"
      body=$(jq -n \
        --arg st "$source_type" --arg sid "$source_id" --arg uid "$user_id" --arg at "$collected_at" --arg c "$cat_val" \
        '{source_type: $st, source_id: $sid, field_id: "device", field_label: "Device used", field_type: "categorical", value_text: $c, user_identifier: $uid, collected_at: $at}')
      ;;
    4)
      # Rating (1-5)
      rating=$((i % 5 + 1))
      body=$(jq -n \
        --arg st "$source_type" --arg sid "$source_id" --arg uid "$user_id" --arg at "$collected_at" \
        --argjson r "$rating" \
        '{source_type: $st, source_id: $sid, field_id: "feature_rating", field_label: "Rate the feature", field_type: "rating", value_number: $r, user_identifier: $uid, collected_at: $at}')
      ;;
    5)
      # Number (e.g. scale)
      num=$((i % 10 + 1))
      body=$(jq -n \
        --arg st "$source_type" --arg sid "$source_id" --arg uid "$user_id" --arg at "$collected_at" \
        --argjson n "$num" \
        '{source_type: $st, source_id: $sid, field_id: "effort_score", field_label: "Effort (1-10)", field_type: "number", value_number: $n, user_identifier: $uid, collected_at: $at}')
      ;;
    6)
      # Boolean
      bool=$([ $((i % 2)) -eq 0 ] && echo true || echo false)
      body=$(jq -n \
        --arg st "$source_type" --arg sid "$source_id" --arg uid "$user_id" --arg at "$collected_at" \
        --argjson b "$bool" \
        '{source_type: $st, source_id: $sid, field_id: "would_recommend", field_label: "Would you recommend?", field_type: "boolean", value_boolean: $b, user_identifier: $uid, collected_at: $at}')
      ;;
    7)
      # With metadata and optional tenant
      score=$((i % 11))
      meta=$(jq -n --arg seg "segment-$((i % 3))" '{customer_segment: $seg, campaign_id: "seed-batch"}')
      payload=$(jq -n \
        --arg st "$source_type" --arg sid "$source_id" --arg uid "$user_id" --arg at "$collected_at" \
        --argjson n "$score" --argjson m "$meta" \
        '{source_type: $st, source_id: $sid, field_id: "nps_score", field_type: "nps", value_number: $n, user_identifier: $uid, collected_at: $at, metadata: $m}')
      if [ -n "$TENANT" ]; then
        body=$(echo "$payload" | jq --arg tid "$TENANT" '. + {tenant_id: $tid}')
      else
        body="$payload"
      fi
      ;;
  esac

  if post "$body" >/dev/null 2>&1; then
    created=$((created + 1))
    printf "."
  else
    failed=$((failed + 1))
    printf "x"
  fi
done

echo ""
echo "Done. Created: $created, Failed: $failed"
