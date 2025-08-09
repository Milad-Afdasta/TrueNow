# TrueNow Analytics Platform - Complete User Manual

## Table of Contents
1. [Overview](#overview)
2. [Prerequisites](#prerequisites)
3. [Building the Platform](#building-the-platform)
4. [Starting Services](#starting-services)
5. [Data Ingestion](#data-ingestion)
6. [Querying Data](#querying-data)
7. [Namespace & Table Management](#namespace--table-management)
8. [API Reference](#api-reference)
9. [Monitoring](#monitoring)
10. [Troubleshooting](#troubleshooting)
11. [Advanced Configuration](#advanced-configuration)

---

## Overview

TrueNow Analytics Platform is a hyperscale real-time analytics system designed to handle **1 billion queries per second** with microsecond precision timestamps. The platform processes streaming data through a distributed pipeline and provides sub-second query capabilities.

### Architecture Components

| Service | HTTP Port | gRPC Port | Purpose |
|---------|-----------|-----------|----------|
| **Control Plane** | 8001 | 9080 | Metadata, configuration, service discovery |
| **Gateway** | 8088 | 9088 | Event ingestion, validation, rate limiting |
| **Hot Tier** | 9090 | 9091 | In-memory storage, aggregation |
| **Query API** | 8081 | 9081 | Query interface, routing |
| **Stream Ingester** | 8100 | 9100 | Kafka consumer, event processing |
| **Watermark Service** | 8084 | 9084 | Data freshness tracking |
| **Rebalancer** | 8086 | 9086 | Load distribution |

### Data Flow
```
Client → Gateway → Kafka → Stream Ingester → Hot Tier → Query API → Client
```

---

## Prerequisites

### Required Software
```bash
# Check prerequisites
go version          # Go 1.21+ required
docker --version    # Docker 20+ required
psql --version      # PostgreSQL client required
redis-cli --version # Redis client required

# Install if missing (macOS example)
brew install go
brew install postgresql
brew install redis
brew install kafka  # or use Docker
```

### Infrastructure Services
```bash
# PostgreSQL (for metadata)
brew services start postgresql
createdb analytics

# Redis (for caching)
brew services start redis

# Kafka/Redpanda (for streaming)
docker run -d --name redpanda \
  -p 9092:9092 \
  -p 9644:9644 \
  docker.redpanda.com/redpandadata/redpanda:latest \
  redpanda start --overprovisioned --smp 2
```

---

## Building the Platform

### Method 1: Build All Services at Once
```bash
# Build all services with the build script
./build.sh

# This creates binaries in the build/ directory:
# build/control-plane
# build/gateway
# build/hot-tier
# build/query-api
# build/stream-ingester
# build/watermark-service
# build/rebalancer
```

### Method 2: Build Individual Services
```bash
# Build specific service
cd services/gateway
GOWORK=off go build -o ../../build/gateway cmd/main.go

# Build with optimizations
GOWORK=off CGO_ENABLED=0 go build -ldflags="-s -w" \
  -o ../../build/gateway cmd/main.go
```

### Method 3: Using Make (if available)
```bash
# Build everything
make build-all

# Build specific service
make build-gateway
make build-hot-tier
```

---

## Starting Services

### Method 1: Start All Services (Recommended)
```bash
# Start all services with proper dependencies
./start-services.sh

# This will:
# 1. Check prerequisites (PostgreSQL, Redis, Kafka)
# 2. Start services in dependency order
# 3. Wait for health checks
# 4. Display status
```

### Method 2: Manual Service Startup
```bash
# Start in this specific order:

# 1. Control Plane (metadata service)
./build/control-plane > logs/control-plane.log 2>&1 &

# 2. Hot Tier (storage layer)
./build/hot-tier > logs/hot-tier.log 2>&1 &

# 3. Gateway (ingestion)
./build/gateway > logs/gateway.log 2>&1 &

# 4. Stream Ingester (Kafka consumer)
./build/stream-ingester > logs/stream-ingester.log 2>&1 &

# 5. Query API (query interface)
./build/query-api > logs/query-api.log 2>&1 &

# 6. Watermark Service (tracking)
./build/watermark-service > logs/watermark-service.log 2>&1 &

# 7. Rebalancer (load management)
./build/rebalancer > logs/rebalancer.log 2>&1 &
```

### Method 3: Using the Management Script
```bash
# Start platform
./manage.sh start

# Stop platform
./manage.sh stop

# Restart platform
./manage.sh restart

# Check status
./manage.sh status
```

### Verify Services are Running
```bash
# Check health endpoints
curl http://localhost:8001/health  # Control Plane
curl http://localhost:8088/health  # Gateway
curl http://localhost:9090/health  # Hot Tier
curl http://localhost:8081/health  # Query API
curl http://localhost:8084/health  # Watermark Service
curl http://localhost:8086/health  # Rebalancer

# Check processes
ps aux | grep "build/"

# Check logs for errors
grep -i error logs/*.log
```

---

## Data Ingestion

### Understanding Timestamps
**IMPORTANT**: All timestamps use **microseconds** (1 second = 1,000,000 microseconds)

```bash
# Get current time in microseconds
echo $(($(date +%s) * 1000000))
```

### Single Event Ingestion
```bash
# Basic event
curl -X POST http://localhost:8088/v1/ingest \
  -H "Content-Type: application/json" \
  -d '{
    "namespace": "production",
    "table": "events",
    "event_id": "evt-001",
    "event_time": '$(($(date +%s) * 1000000))',
    "dimensions": {
      "region": "us-east",
      "product": "widget-a",
      "customer_type": "premium"
    },
    "metrics": {
      "revenue": 99.99,
      "quantity": 3,
      "processing_time": 45.2
    }
  }'
```

### Batch Event Ingestion (More Efficient)
```bash
# Send multiple events
curl -X POST http://localhost:8088/v1/batch \
  -H "Content-Type: application/json" \
  -d '{
    "namespace": "production",
    "table": "events",
    "events": [
      {
        "event_id": "evt-001",
        "event_time": '$(($(date +%s) * 1000000))',
        "dimensions": {"region": "us-east", "product": "widget-a"},
        "metrics": {"revenue": 99.99, "quantity": 3}
      },
      {
        "event_id": "evt-002",
        "event_time": '$(($(date +%s) * 1000000))',
        "dimensions": {"region": "eu-west", "product": "widget-b"},
        "metrics": {"revenue": 149.99, "quantity": 5}
      }
    ]
  }'
```

### High-Volume Data Population
```bash
# Use the populate script for testing
./populate_test_data.sh

# This generates:
# - 1000+ events
# - Multiple namespaces (ecommerce, analytics, monitoring, telemetry)
# - Multiple tables (events, metrics, logs, traces)
# - Various dimensions and metrics
```

### Custom Data Generator
```bash
# Generate continuous stream of events
while true; do
  curl -X POST http://localhost:8088/v1/ingest \
    -H "Content-Type: application/json" \
    -d '{
      "namespace": "live",
      "table": "streams",
      "event_id": "'$(uuidgen)'",
      "event_time": '$(($(date +%s) * 1000000))',
      "dimensions": {
        "source": "generator",
        "type": "synthetic"
      },
      "metrics": {
        "value": '$((RANDOM % 1000))'
      }
    }'
  sleep 0.1  # 10 events per second
done
```

---

## Querying Data

### Basic Time Range Query
```bash
# Query last hour of data
curl -X POST http://localhost:8081/v1/query \
  -H "Content-Type: application/json" \
  -d '{
    "start_time": '$(($(date +%s) * 1000000 - 3600000000))',
    "end_time": '$(($(date +%s) * 1000000))',
    "namespace": "production",
    "table": "events"
  }'
```

### Filtered Queries
```bash
# Filter by dimensions
curl -X POST http://localhost:8081/v1/query \
  -H "Content-Type: application/json" \
  -d '{
    "start_time": '$(($(date +%s) * 1000000 - 3600000000))',
    "end_time": '$(($(date +%s) * 1000000))',
    "namespace": "production",
    "table": "events",
    "filters": {
      "region": "us-east",
      "product": "widget-a"
    }
  }'
```

### Aggregation Queries
```bash
# Sum revenue by region
curl -X POST http://localhost:8081/v1/query \
  -H "Content-Type: application/json" \
  -d '{
    "start_time": '$(($(date +%s) * 1000000 - 3600000000))',
    "end_time": '$(($(date +%s) * 1000000))',
    "namespace": "production",
    "table": "events",
    "group_by": ["region"],
    "aggregate": "sum",
    "metric": "revenue"
  }'

# Average processing time by product
curl -X POST http://localhost:8081/v1/query \
  -H "Content-Type: application/json" \
  -d '{
    "start_time": '$(($(date +%s) * 1000000 - 3600000000))',
    "end_time": '$(($(date +%s) * 1000000))',
    "namespace": "production",
    "table": "events",
    "group_by": ["product"],
    "aggregate": "avg",
    "metric": "processing_time"
  }'
```

### Complex Queries
```bash
# Multi-dimension grouping with filters
curl -X POST http://localhost:8081/v1/query \
  -H "Content-Type: application/json" \
  -d '{
    "start_time": '$(($(date +%s) * 1000000 - 86400000000))',
    "end_time": '$(($(date +%s) * 1000000))',
    "namespace": "production",
    "table": "events",
    "filters": {
      "customer_type": "premium"
    },
    "group_by": ["region", "product"],
    "aggregate": "count"
  }'
```

### Query Response Format
```json
{
  "results": [
    {
      "timestamp": 1754772362000000,
      "group": "region:us-east",
      "count": 42,
      "sum": 4199.58,
      "avg": 99.99,
      "min": 49.99,
      "max": 149.99
    }
  ],
  "metadata": {
    "query_time_ms": 45,
    "records_scanned": 10000,
    "cache_hit": false
  }
}
```

---

## Namespace & Table Management

### Understanding Namespaces and Tables

Namespaces and tables provide logical organization of your data:
- **Namespace**: Top-level container (e.g., production, staging, analytics)
- **Table**: Data collection within namespace (e.g., events, metrics, logs)

### Creating Namespaces and Tables

Namespaces and tables are **created automatically** when you send data. No pre-registration required!

```bash
# This automatically creates namespace "mycompany" and table "sales"
curl -X POST http://localhost:8088/v1/ingest \
  -H "Content-Type: application/json" \
  -d '{
    "namespace": "mycompany",
    "table": "sales",
    "event_id": "sale-001",
    "event_time": '$(($(date +%s) * 1000000))',
    "dimensions": {
      "store": "NYC-001",
      "category": "electronics"
    },
    "metrics": {
      "amount": 599.99,
      "items": 1
    }
  }'
```

### Best Practices for Organization

```bash
# Namespace examples:
# - By environment: production, staging, development
# - By department: sales, marketing, engineering
# - By application: web-app, mobile-app, api
# - By region: us-east, eu-west, ap-south

# Table examples:
# - By data type: events, metrics, logs, traces
# - By source: clickstream, transactions, sensors
# - By granularity: raw, aggregated, summary
```

### Example: Multi-Tenant Setup
```bash
# Tenant 1: E-commerce Company
curl -X POST http://localhost:8088/v1/ingest \
  -d '{
    "namespace": "ecommerce_prod",
    "table": "orders",
    "event_id": "ord-12345",
    "event_time": '$(($(date +%s) * 1000000))',
    "dimensions": {"channel": "web", "country": "US"},
    "metrics": {"total": 299.99, "items": 3}
  }'

# Tenant 2: IoT Platform
curl -X POST http://localhost:8088/v1/ingest \
  -d '{
    "namespace": "iot_sensors",
    "table": "temperature",
    "event_id": "sensor-001-read",
    "event_time": '$(($(date +%s) * 1000000))',
    "dimensions": {"location": "warehouse-1", "sensor_id": "TMP-001"},
    "metrics": {"celsius": 22.5, "humidity": 65}
  }'

# Tenant 3: Gaming Platform
curl -X POST http://localhost:8088/v1/ingest \
  -d '{
    "namespace": "gaming",
    "table": "player_events",
    "event_id": "evt-player-action",
    "event_time": '$(($(date +%s) * 1000000))',
    "dimensions": {"game": "space-shooter", "level": "5"},
    "metrics": {"score": 15000, "time_played": 300}
  }'
```

### Listing Available Namespaces and Tables
```bash
# Get system statistics (includes namespace info)
curl http://localhost:8081/v1/stats

# Query specific namespace to see if it has data
curl -X POST http://localhost:8081/v1/query \
  -d '{
    "namespace": "mycompany",
    "table": "sales",
    "start_time": 0,
    "end_time": '$(($(date +%s) * 1000000))'
  }'
```

---

## API Reference

### Gateway API (Port 8088)

#### POST /v1/ingest
Ingest a single event.

**Request:**
```json
{
  "namespace": "string (required)",
  "table": "string (required)",
  "event_id": "string (required)",
  "event_time": "number (microseconds, required)",
  "dimensions": {
    "key1": "value1",
    "key2": "value2"
  },
  "metrics": {
    "metric1": 123.45,
    "metric2": 67
  }
}
```

**Response:**
```json
{
  "status": "accepted",
  "event_id": "evt-001"
}
```

#### POST /v1/batch
Ingest multiple events.

**Request:**
```json
{
  "namespace": "string (required)",
  "table": "string (required)",
  "events": [
    {
      "event_id": "string",
      "event_time": "number (microseconds)",
      "dimensions": {},
      "metrics": {}
    }
  ]
}
```

**Response:**
```json
{
  "status": "accepted",
  "events_received": 100,
  "events_accepted": 100
}
```

### Query API (Port 8081)

#### POST /v1/query
Execute a query.

**Request:**
```json
{
  "namespace": "string (required)",
  "table": "string (required)",
  "start_time": "number (microseconds, required)",
  "end_time": "number (microseconds, required)",
  "filters": {
    "dimension": "value"
  },
  "group_by": ["dimension1", "dimension2"],
  "aggregate": "sum|avg|min|max|count",
  "metric": "metric_name"
}
```

**Response:**
```json
{
  "results": [
    {
      "timestamp": 1234567890000000,
      "group": "dimension:value",
      "value": 123.45,
      "count": 10
    }
  ],
  "metadata": {
    "query_time_ms": 45
  }
}
```

#### GET /v1/stats
Get system statistics.

**Response:**
```json
{
  "namespaces": ["production", "staging"],
  "total_events": 1000000,
  "events_per_second": 10000,
  "uptime_seconds": 3600
}
```

### Health Check Endpoints

All services expose health endpoints:
```bash
# Health check format
GET http://[service-host]:[port]/health

# Response
{
  "status": "healthy",
  "service": "gateway",
  "uptime": 3600,
  "version": "1.0.0"
}
```

---

## Monitoring

### Service Health Monitoring
```bash
# Check all services at once
for port in 8001 8088 9090 8081 8084 8086; do
  echo "Checking port $port:"
  curl -s http://localhost:$port/health | jq .
done
```

### Log Monitoring
```bash
# Watch logs in real-time
tail -f logs/gateway.log

# Check for errors across all services
grep -i error logs/*.log

# Monitor specific service
tail -f logs/hot-tier.log | grep -v "health"
```

### Metrics and Performance
```bash
# Get query performance stats
curl http://localhost:8081/v1/stats

# Monitor ingestion rate
watch -n 1 'curl -s http://localhost:8088/metrics | grep ingest_rate'

# Check memory usage
ps aux | grep "build/" | awk '{sum+=$6} END {print "Total Memory (MB):", sum/1024}'
```

### Using the Monitor Tool
```bash
# Real-time system monitor
cd services/monitor
./monitor --simulate

# This shows:
# - Service status
# - Event rates
# - Query latencies
# - Resource usage
# - Watermarks
```

---

## Troubleshooting

### Common Issues and Solutions

#### Services Won't Start
```bash
# Check if ports are already in use
lsof -i :8088  # Gateway port
lsof -i :8081  # Query API port

# Kill existing processes
pkill -f "build/"

# Check prerequisites
pg_isready  # PostgreSQL
redis-cli ping  # Redis
```

#### No Query Results
```bash
# 1. Check if data is being ingested
tail -f logs/gateway.log | grep "accepted"

# 2. Check stream ingester is processing
tail -f logs/stream-ingester.log

# 3. Verify hot-tier has data
curl http://localhost:9090/stats

# 4. Check time range (remember: microseconds!)
echo "Current time in microseconds: $(($(date +%s) * 1000000))"
```

#### High Memory Usage
```bash
# Check which service is using memory
ps aux | grep "build/" | sort -k6 -n

# Restart specific service
pkill -f "hot-tier" && ./build/hot-tier &

# Clear Redis cache
redis-cli FLUSHALL
```

#### Gateway Crashes
```bash
# Check rate limiter issues
grep -i "panic\|fatal" logs/gateway.log

# Restart with increased limits
RATE_LIMIT=100000 ./build/gateway &
```

### Debug Mode
```bash
# Run services with debug logging
DEBUG=true ./build/gateway

# Enable verbose logging
LOG_LEVEL=debug ./build/query-api
```

### Performance Issues
```bash
# Check Kafka lag
docker exec redpanda rpk topic consume events --offset start | head -10

# Monitor CPU usage
top -p $(pgrep -d, -f "build/")

# Check disk I/O
iotop -p $(pgrep -d, -f "build/")
```

---

## Advanced Configuration

### Environment Variables
```bash
# Gateway configuration
export GATEWAY_PORT=8088
export GATEWAY_RATE_LIMIT=1000000
export GATEWAY_BATCH_SIZE=1000

# Hot-tier configuration
export HOT_TIER_PORT=9090
export HOT_TIER_MEMORY_GB=32
export HOT_TIER_RETENTION_HOURS=24

# Query API configuration
export QUERY_API_PORT=8081
export QUERY_API_CACHE_TTL=60
export QUERY_API_MAX_RESULTS=10000
```

### Configuration Files
```bash
# Create config directory
mkdir -p config

# Gateway config
cat > config/gateway.yaml <<EOF
port: 8088
rate_limit:
  global: 1000000
  per_tenant: 100000
batch:
  max_size: 1000
  timeout_ms: 100
EOF

# Hot-tier config
cat > config/hot-tier.yaml <<EOF
port: 9090
storage:
  memory_gb: 32
  retention_hours: 24
  compaction_interval: 300
aggregation:
  window_size: 60
  dimensions_limit: 1000
EOF
```

### Scaling Configuration
```bash
# Horizontal scaling (multiple instances)
# Start multiple gateways on different ports
GATEWAY_PORT=8088 ./build/gateway &
GATEWAY_PORT=8089 ./build/gateway &
GATEWAY_PORT=8090 ./build/gateway &

# Load balance with nginx
cat > /etc/nginx/sites-available/truenow <<EOF
upstream gateway {
  server localhost:8088;
  server localhost:8089;
  server localhost:8090;
}
server {
  listen 80;
  location /v1/ingest {
    proxy_pass http://gateway;
  }
}
EOF
```

### Production Deployment
```bash
# SystemD service file
cat > /etc/systemd/system/truenow-gateway.service <<EOF
[Unit]
Description=TrueNow Gateway Service
After=network.target

[Service]
Type=simple
User=truenow
WorkingDirectory=/opt/truenow
ExecStart=/opt/truenow/build/gateway
Restart=always
RestartSec=10

[Install]
WantedBy=multi-user.target
EOF

# Enable and start
systemctl enable truenow-gateway
systemctl start truenow-gateway
```

---

## Quick Command Reference

```bash
# Building
./build.sh                      # Build all services

# Starting/Stopping
./start-services.sh             # Start all services
pkill -f "build/"              # Stop all services

# Data Operations
./populate_test_data.sh        # Populate test data
./test_queries.sh              # Run query tests

# Monitoring
tail -f logs/*.log             # Watch all logs
grep -i error logs/*.log       # Check for errors
ps aux | grep "build/"         # Check processes

# Health Checks
curl http://localhost:8088/health  # Gateway health
curl http://localhost:8081/health  # Query API health

# Quick Ingestion Test
curl -X POST http://localhost:8088/v1/ingest \
  -d '{"namespace":"test","table":"events","event_id":"1","event_time":'$(($(date +%s)*1000000))'}'

# Quick Query Test  
curl -X POST http://localhost:8081/v1/query \
  -d '{"namespace":"test","table":"events","start_time":0,"end_time":'$(($(date +%s)*1000000))'}'
```

---

## Support and Resources

- **Logs Location**: `logs/` directory
- **Build Output**: `build/` directory
- **Test Scripts**: `populate_test_data.sh`, `test_queries.sh`
- **Configuration**: Environment variables or config files
- **Default Ports**: Gateway (8088), Query API (8081), Control Plane (8001)

For production deployments, ensure you have:
- Sufficient memory (32GB+ recommended for hot-tier)
- Fast SSD storage for Kafka/Redpanda
- Network capacity for expected throughput
- Monitoring and alerting configured

---

*Last Updated: 2025*
*Platform Version: 1.0.0*
*Timestamp Precision: Microseconds (μs)*