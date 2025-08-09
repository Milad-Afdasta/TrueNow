#!/bin/bash

# Populate TrueNow Analytics Platform with diverse test data
echo "Starting data population..."

# Configuration
GATEWAY_URL="http://localhost:8088/v1/ingest"
NAMESPACES=("ecommerce" "analytics" "monitoring" "telemetry")
TABLES=("events" "metrics" "logs" "traces")
REGIONS=("us-east" "us-west" "eu-west" "ap-south")
STATUSES=("active" "pending" "completed" "failed")
SERVERS=("srv-01" "srv-02" "srv-03" "srv-04" "srv-05")
PRODUCTS=("product-a" "product-b" "product-c" "product-d")

# Function to send event
send_event() {
  local namespace=$1
  local table=$2
  local event_id=$3
  local timestamp=$4
  local region=$5
  local status=$6
  local server=$7
  local product=$8
  local latency=$9
  local throughput=${10}
  local cpu=${11}
  local memory=${12}
  
  curl -s -X POST "$GATEWAY_URL" \
    -H "Content-Type: application/json" \
    -H "X-Namespace: $namespace" \
    -H "X-Table: $table" \
    -d "{
      \"event_id\": \"$event_id\",
      \"event_time\": $timestamp,
      \"dims\": {
        \"region\": \"$region\",
        \"status\": \"$status\",
        \"server\": \"$server\",
        \"product\": \"$product\"
      },
      \"metrics\": {
        \"latency\": $latency,
        \"throughput\": $throughput,
        \"cpu_usage\": $cpu,
        \"memory_usage\": $memory
      }
    }" > /dev/null
}

# Generate 1000 events over the last hour
echo "Generating 1000 events..."
current_time=$(date +%s)
events_sent=0

for i in {1..1000}; do
  # Random selections
  namespace=${NAMESPACES[$((RANDOM % ${#NAMESPACES[@]}))]}
  table=${TABLES[$((RANDOM % ${#TABLES[@]}))]}
  region=${REGIONS[$((RANDOM % ${#REGIONS[@]}))]}
  status=${STATUSES[$((RANDOM % ${#STATUSES[@]}))]}
  server=${SERVERS[$((RANDOM % ${#SERVERS[@]}))]}
  product=${PRODUCTS[$((RANDOM % ${#PRODUCTS[@]}))]}
  
  # Time spread over last hour (in microseconds)
  time_offset=$((RANDOM % 3600))
  timestamp=$(echo "($current_time - $time_offset) * 1000000" | bc)
  
  # Random metrics
  latency=$(echo "scale=2; $RANDOM / 100" | bc)
  throughput=$((RANDOM % 10000 + 1000))
  cpu=$(echo "scale=2; $RANDOM / 328" | bc)  # 0-100
  memory=$(echo "scale=2; $RANDOM / 328" | bc)  # 0-100
  
  event_id="test-$(date +%s%N)-$i"
  
  send_event "$namespace" "$table" "$event_id" "$timestamp" \
    "$region" "$status" "$server" "$product" \
    "$latency" "$throughput" "$cpu" "$memory"
  
  events_sent=$((events_sent + 1))
  
  # Progress indicator every 100 events
  if [ $((events_sent % 100)) -eq 0 ]; then
    echo "  Sent $events_sent/1000 events..."
  fi
done

# Send some recent events for immediate testing
echo "Sending 50 recent events for immediate queries..."
for i in {1..50}; do
  namespace=${NAMESPACES[$((RANDOM % ${#NAMESPACES[@]}))]}
  table=${TABLES[$((RANDOM % ${#TABLES[@]}))]}
  region=${REGIONS[$((RANDOM % ${#REGIONS[@]}))]}
  status="active"  # Use consistent status for easier testing
  server=${SERVERS[$((RANDOM % ${#SERVERS[@]}))]}
  product=${PRODUCTS[$((RANDOM % ${#PRODUCTS[@]}))]}
  
  # Very recent events (last 10 seconds)
  timestamp=$(echo "$(date +%s) * 1000000" | bc)
  
  latency=$(echo "scale=2; $RANDOM / 100" | bc)
  throughput=$((RANDOM % 10000 + 1000))
  cpu=$(echo "scale=2; $RANDOM / 328" | bc)
  memory=$(echo "scale=2; $RANDOM / 328" | bc)
  
  event_id="recent-$(date +%s%N)-$i"
  
  send_event "$namespace" "$table" "$event_id" "$timestamp" \
    "$region" "$status" "$server" "$product" \
    "$latency" "$throughput" "$cpu" "$memory"
done

echo "✅ Data population complete! Sent 1050 events across:"
echo "  - Namespaces: ${NAMESPACES[*]}"
echo "  - Tables: ${TABLES[*]}"
echo "  - Regions: ${REGIONS[*]}"
echo "  - Time range: Last hour + 50 recent events"