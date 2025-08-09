#!/bin/bash

# Real-Time Analytics Platform - Service Test Script
# This script tests each service individually

set -e

echo "======================================"
echo "Real-Time Analytics Platform Test"
echo "======================================"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Function to check if service is running
check_service() {
    local service=$1
    local port=$2
    
    if nc -z localhost $port 2>/dev/null; then
        echo -e "${GREEN}✓${NC} $service is running on port $port"
        return 0
    else
        echo -e "${RED}✗${NC} $service is not running on port $port"
        return 1
    fi
}

# Function to test HTTP endpoint
test_http() {
    local service=$1
    local url=$2
    local expected_code=$3
    
    response=$(curl -s -o /dev/null -w "%{http_code}" $url 2>/dev/null || echo "000")
    
    if [ "$response" = "$expected_code" ]; then
        echo -e "${GREEN}✓${NC} $service HTTP endpoint responded with $response"
        return 0
    else
        echo -e "${RED}✗${NC} $service HTTP endpoint failed (got $response, expected $expected_code)"
        return 1
    fi
}

echo ""
echo "1. Checking Prerequisites..."
echo "----------------------------"

# Check PostgreSQL
if check_service "PostgreSQL" 5432; then
    # Test connection
    if PGPASSWORD=analytics123 psql -h localhost -U analytics -d analytics -c "SELECT 1" > /dev/null 2>&1; then
        echo -e "${GREEN}✓${NC} PostgreSQL connection successful"
    else
        echo -e "${YELLOW}!${NC} PostgreSQL running but cannot connect (may need setup)"
    fi
fi

# Check Redis
if check_service "Redis" 6379; then
    # Test connection
    if redis-cli ping > /dev/null 2>&1; then
        echo -e "${GREEN}✓${NC} Redis connection successful"
    fi
fi

# Check Redpanda/Kafka
check_service "Redpanda/Kafka" 9092

echo ""
echo "2. Testing Control Plane Service..."
echo "------------------------------------"

# Start control plane if not running
if ! check_service "Control Plane" 8001; then
    echo "Starting Control Plane service..."
    cd services/control-plane
    ./bin/control-plane > control-plane.log 2>&1 &
    CONTROL_PLANE_PID=$!
    sleep 3
    
    if check_service "Control Plane" 8001; then
        echo -e "${GREEN}✓${NC} Control Plane started (PID: $CONTROL_PLANE_PID)"
    fi
fi

# Test control plane endpoints
test_http "Control Plane" "http://localhost:8001/health" "200"

# Create test namespace
echo ""
echo "Creating test namespace..."
curl -X POST http://localhost:8001/v1/namespaces \
    -H "Content-Type: application/json" \
    -d '{
        "name": "test_namespace",
        "description": "Test namespace for validation",
        "retention_hours": 24,
        "settings": {
            "ingestion_rate_limit": 10000,
            "enable_dedup": true
        }
    }' 2>/dev/null

if [ $? -eq 0 ]; then
    echo -e "${GREEN}✓${NC} Test namespace created"
else
    echo -e "${YELLOW}!${NC} Namespace may already exist"
fi

echo ""
echo "3. Testing Gateway Service..."
echo "------------------------------"

# Start gateway if not running
if ! check_service "Gateway" 8080; then
    echo "Starting Gateway service..."
    cd services/gateway
    ./bin/gateway > gateway.log 2>&1 &
    GATEWAY_PID=$!
    sleep 3
    
    if check_service "Gateway" 8080; then
        echo -e "${GREEN}✓${NC} Gateway started (PID: $GATEWAY_PID)"
    fi
fi

# Test gateway ingestion endpoint
echo ""
echo "Sending test event to gateway..."
response=$(curl -X POST http://localhost:8080/v1/ingest \
    -H "Content-Type: application/json" \
    -H "X-Namespace: test_namespace" \
    -H "X-Table: events" \
    -d '[{
        "event_id": "test_001",
        "event_time": '$(date +%s000)',
        "namespace": "test_namespace",
        "table": "events",
        "revision": 1,
        "dims": {
            "country": "US",
            "device": "mobile"
        },
        "metrics": {
            "revenue": 10.50,
            "impressions": 100
        }
    }]' -s -w "\nHTTP Status: %{http_code}\n" 2>/dev/null)

if [[ $response == *"202"* ]] || [[ $response == *"200"* ]]; then
    echo -e "${GREEN}✓${NC} Event accepted by gateway"
else
    echo -e "${RED}✗${NC} Event rejected by gateway"
    echo "$response"
fi

echo ""
echo "4. Testing Hot Tier Service..."
echo "-------------------------------"

# Start hot tier if not running
if ! check_service "Hot Tier" 9090; then
    echo "Starting Hot Tier service..."
    cd services/hot-tier
    SHARD_ID=0 ./bin/hot-tier > hot-tier.log 2>&1 &
    HOT_TIER_PID=$!
    sleep 3
    
    if check_service "Hot Tier" 9090; then
        echo -e "${GREEN}✓${NC} Hot Tier started (PID: $HOT_TIER_PID)"
    fi
fi

echo ""
echo "5. Service Summary"
echo "------------------"

services_ok=0
services_total=0

for service in "PostgreSQL:5432" "Redis:6379" "Redpanda:9092" "Control-Plane:8001" "Gateway:8080" "Hot-Tier:9090"; do
    IFS=':' read -r name port <<< "$service"
    services_total=$((services_total + 1))
    if nc -z localhost $port 2>/dev/null; then
        echo -e "${GREEN}✓${NC} $name"
        services_ok=$((services_ok + 1))
    else
        echo -e "${RED}✗${NC} $name"
    fi
done

echo ""
echo "======================================"
echo "Test Results: $services_ok/$services_total services running"
echo "======================================"

if [ $services_ok -eq $services_total ]; then
    echo -e "${GREEN}All services are operational!${NC}"
    echo ""
    echo "Next steps:"
    echo "1. Check logs: tail -f services/*/**.log"
    echo "2. Monitor metrics: http://localhost:8090/metrics (Hot Tier)"
    echo "3. Send more test data using the gateway endpoint"
    echo "4. Check Redpanda console: http://localhost:8083 (if running)"
else
    echo -e "${YELLOW}Some services are not running.${NC}"
    echo "Check the logs for errors:"
    echo "  - Control Plane: services/control-plane/control-plane.log"
    echo "  - Gateway: services/gateway/gateway.log"
    echo "  - Hot Tier: services/hot-tier/hot-tier.log"
fi

# Cleanup function (optional)
cleanup() {
    echo ""
    echo "Stopping test services..."
    [ ! -z "$CONTROL_PLANE_PID" ] && kill $CONTROL_PLANE_PID 2>/dev/null
    [ ! -z "$GATEWAY_PID" ] && kill $GATEWAY_PID 2>/dev/null
    [ ! -z "$HOT_TIER_PID" ] && kill $HOT_TIER_PID 2>/dev/null
    echo "Done."
}

# Uncomment to enable cleanup on exit
# trap cleanup EXIT

echo ""
echo "Press Ctrl+C to stop test services..."