#!/bin/bash

# Test queries for TrueNow Analytics Platform
QUERY_API="http://localhost:8081/v1/query"

echo "==========================================="
echo "Testing TrueNow Analytics Platform Queries"
echo "==========================================="

# Function to run query and check result
run_query() {
  local test_name=$1
  local query_json=$2
  
  echo ""
  echo "Test: $test_name"
  echo "Query: $query_json"
  
  response=$(curl -s -X POST "$QUERY_API" \
    -H "Content-Type: application/json" \
    -d "$query_json")
  
  # Check if response is valid JSON
  if echo "$response" | python3 -m json.tool > /dev/null 2>&1; then
    result_count=$(echo "$response" | python3 -c "import sys, json; data=json.load(sys.stdin); print(len(data.get('results', [])))" 2>/dev/null)
    if [ -n "$result_count" ]; then
      echo "✅ Success: Query returned $result_count results"
      if [ "$result_count" -gt 0 ]; then
        echo "Sample result:"
        echo "$response" | python3 -c "
import sys, json
data = json.load(sys.stdin)
if data.get('results'):
    r = data['results'][0]
    print(f'  Timestamp: {r.get(\"timestamp\", \"N/A\")} μs')
    print(f'  Group: {r.get(\"group\", \"N/A\")}')
    print(f'  Count: {r.get(\"count\", \"N/A\")}')
" 2>/dev/null
      fi
    else
      echo "❌ Failed: Invalid response format"
      echo "Response: $response"
    fi
  else
    echo "❌ Failed: $response"
  fi
}

# Calculate time ranges in microseconds
current_time=$(date +%s)
now_us=$(echo "$current_time * 1000000" | bc)
one_minute_ago=$(echo "$now_us - 60000000" | bc)
five_minutes_ago=$(echo "$now_us - 300000000" | bc)
one_hour_ago=$(echo "$now_us - 3600000000" | bc)

# Test 1: Basic time range query (last minute)
run_query "Basic time range - last minute" "{
  \"start_time\": $one_minute_ago,
  \"end_time\": $now_us,
  \"namespace\": \"analytics\",
  \"table\": \"events\"
}"

# Test 2: Basic time range query (last 5 minutes)
run_query "Basic time range - last 5 minutes" "{
  \"start_time\": $five_minutes_ago,
  \"end_time\": $now_us,
  \"namespace\": \"ecommerce\",
  \"table\": \"metrics\"
}"

# Test 3: Basic time range query (last hour)
run_query "Basic time range - last hour" "{
  \"start_time\": $one_hour_ago,
  \"end_time\": $now_us,
  \"namespace\": \"monitoring\",
  \"table\": \"logs\"
}"

echo ""
echo "==========================================="
echo "Basic time range query tests completed"
echo "==========================================="