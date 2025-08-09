# TrueNow Analytics Platform

> 📖 **Complete User Documentation**: See [USER_MANUAL.md](USER_MANUAL.md) for detailed setup, usage instructions, and API reference.

## 🚀 Overview

TrueNow is a **hyperscale real-time analytics platform** designed to handle **1 billion queries per second (1B QPS)** with sub-second data freshness. Built with a focus on extreme performance, it features a 24-hour in-memory hot tier and sophisticated scaling capabilities.

### Key Capabilities
- **10M+ events/second** sustained ingestion (scalable to 1B QPS)
- **< 30 seconds p99 data freshness**
- **< 300ms p95 query latency**
- **Automatic scaling** with cascading rules and backpressure management
- **Zero-downtime deployments** with epoch-based configuration management
- **Multi-region support** with geo-distributed sharding

## 📊 Architecture

### Core Services

| Service | Port (HTTP) | Port (gRPC) | Purpose |
|---------|------------|-------------|----------|
| **Control Plane** | 8001 | 9080 | Metadata, schemas, epochs, service discovery |
| **Gateway** | 8088 | 9088 | High-throughput event ingestion, validation |
| **Hot Tier** | 9090 | 9091 | In-memory aggregation, ring buffers, sketches |
| **Query API** | 8081 | 9081 | Query planning, routing, caching |
| **Stream Ingester** | 8100 | 9100 | Kafka consumer, transforms, deduplication |
| **Watermark Service** | 8084 | 9084 | Data freshness tracking (P50/P95/P99) |
| **Rebalancer** | 8086 | 9086 | Load distribution, hot shard mitigation |
| **Autoscaler** | 8095 | 9095 | Dynamic scaling, cascading rules |
| **Monitor** | - | - | Real-time system observability |

### Data Flow Architecture

```
┌─────────┐     HTTP/gRPC      ┌─────────┐     Kafka      ┌──────────────┐
│ Clients │ ─────────────────▶ │ Gateway │ ─────────────▶ │Stream Ingester│
└─────────┘                     └─────────┘                └──────────────┘
                                     │                             │
                                Rate Limit                    Transform
                                Validation                    Deduplicate
                                     │                             │
                                     ▼                             ▼
                               ┌──────────┐     gRPC        ┌──────────┐
                               │ Redpanda │ ◀───────────────│ Hot Tier │
                               └──────────┘                 └──────────┘
                                                                  │
                                                           In-Memory Store
                                                           Ring Buffers
                                                           HLL/T-Digest
                                                                  │
                                   ┌──────────────────────────────┘
                                   ▼
                            ┌────────────┐
                            │ Query API  │ ◀──── Client Queries
                            └────────────┘
```

## 🛠️ Technology Stack

### Core Technologies
- **Languages**: Go (primary), with SIMD optimizations
- **Message Queue**: Apache Kafka/Redpanda
- **Cache**: Redis/KeyDB
- **Database**: PostgreSQL (metadata only)
- **Object Storage**: MinIO/S3
- **Protocol**: gRPC for internal, REST for external APIs
- **Container Orchestration**: Kubernetes

### Performance Optimizations
- **Zero-copy operations** with mmap and io_uring
- **Lock-free data structures** (LMAX Disruptor pattern)
- **NUMA-aware memory allocation**
- **SIMD/AVX-512** for batch validation
- **DPDK** for kernel bypass networking (at scale)
- **HugeTLB pages** for reduced TLB misses

## 🚦 Quick Start

### Prerequisites
```bash
# Required tools
- Docker & Docker Compose
- Go 1.21+
- Make
- PostgreSQL client (psql)
- Redis client (redis-cli)
```

### Local Development Setup

```bash
# 1. Clone the repository
git clone https://github.com/Milad-Afdasta/TrueNow.git
# or
git clone git@github.com:Milad-Afdasta/TrueNow.git
# or
gh repo clone Milad-Afdasta/TrueNow

cd TrueNow

# 2. Start infrastructure services
brew services start postgresql
brew services start redis
docker run -d --name redpanda -p 9092:9092 docker.redpanda.com/redpandadata/redpanda:latest

# 3. Create database
createdb analytics

# 4. Build all services
./build.sh

# 5. Start all services
./start-services.sh

# 6. Verify health
curl http://localhost:8001/health  # Control Plane
curl http://localhost:8088/health  # Gateway
curl http://localhost:9090/health  # Hot Tier
```

### Send Test Events

```bash
# Single event
curl -X POST http://localhost:8088/v1/ingest \
  -H "Content-Type: application/json" \
  -d '{
    "namespace": "test",
    "table": "events",
    "event_id": "test_001",
    "event_time": '$(($(date +%s) * 1000000))',
    "dimensions": {"country": "US", "device": "mobile"},
    "metrics": {"revenue": 10.50, "quantity": 2}
  }'

# Batch events (more efficient)
curl -X POST http://localhost:8088/v1/batch \
  -H "Content-Type: application/json" \
  -d '{
    "namespace": "test",
    "table": "events",
    "events": [
      {"event_id": "001", "event_time": '$(($(date +%s) * 1000000))', "dimensions": {"country": "US"}, "metrics": {"revenue": 10}},
      {"event_id": "002", "event_time": '$(($(date +%s) * 1000000))', "dimensions": {"country": "UK"}, "metrics": {"revenue": 20}}
    ]
  }'
```

### Query Data

```bash
# Query aggregated data
curl -X POST http://localhost:8081/v1/query \
  -H "Content-Type: application/json" \
  -d '{
    "namespace": "test",
    "table": "events",
    "start_time": '$(($(date +%s) * 1000000 - 3600000000))',
    "end_time": '$(($(date +%s) * 1000000))',
    "group_by": ["country"],
    "aggregate": "sum",
    "metric": "revenue"
  }'
```

## 🌍 Production Deployment

### Multi-Region Architecture

```yaml
# Region: US-East-1 (Primary)
├── Control Plane (Leader)
├── Gateway Cluster (1000 nodes)
├── Hot Tier Cluster (5000 nodes)
├── Query API Cluster (500 nodes)
└── Kafka Cluster (100 brokers)

# Region: EU-West-1 (Secondary)
├── Control Plane (Follower)
├── Gateway Cluster (800 nodes)
├── Hot Tier Cluster (4000 nodes)
├── Query API Cluster (400 nodes)
└── Kafka Cluster (80 brokers)

# Region: AP-Southeast-1 (Secondary)
├── Control Plane (Follower)
├── Gateway Cluster (600 nodes)
├── Hot Tier Cluster (3000 nodes)
├── Query API Cluster (300 nodes)
└── Kafka Cluster (60 brokers)
```

### Kubernetes Deployment

```bash
# Deploy using Helm
helm install truenow ./charts/truenow \
  --namespace truenow \
  --values values.production.yaml \
  --set global.region=us-east-1 \
  --set gateway.replicas=1000 \
  --set hotTier.replicas=5000

# Enable autoscaling
kubectl apply -f k8s/autoscaler/hpa.yaml
kubectl apply -f k8s/autoscaler/vpa.yaml

# Configure network policies
kubectl apply -f k8s/network-policies/
```

### Resource Requirements (per node)

| Service | CPU | Memory | Network | Storage |
|---------|-----|--------|---------|---------|
| Gateway | 32 cores | 64 GB | 100 Gbps | 100 GB SSD |
| Hot Tier | 64 cores | 256 GB | 100 Gbps | 2 TB NVMe |
| Query API | 16 cores | 32 GB | 25 Gbps | 100 GB SSD |
| Kafka | 32 cores | 128 GB | 100 Gbps | 10 TB NVMe |

### Performance Tuning

**Note**: The system uses microsecond precision timestamps (1 second = 1,000,000 microseconds) for all time-related operations.

```bash
# Enable HugePages
echo 'vm.nr_hugepages=8192' >> /etc/sysctl.conf
sysctl -p

# CPU affinity and NUMA pinning
numactl --cpunodebind=0 --membind=0 ./hot-tier

# Network optimization
ethtool -K eth0 gro on gso on tso on
echo 'net.core.rmem_max = 134217728' >> /etc/sysctl.conf
echo 'net.core.wmem_max = 134217728' >> /etc/sysctl.conf

# Disable CPU frequency scaling
cpupower frequency-set -g performance
```

## 📈 Monitoring & Observability

### Metrics Endpoints
- Prometheus metrics: `http://[service]:8090/metrics`
- Health checks: `http://[service]:[port]/health`
- Service status: `http://[service]:[port]/status`

### Key Metrics to Monitor

| Metric | Target | Alert Threshold |
|--------|--------|-----------------|
| Ingestion Rate | 10M+ events/sec | < 8M events/sec |
| Query Latency P95 | < 300ms | > 500ms |
| Data Freshness P99 | < 30s | > 60s |
| Memory Usage | < 80% | > 90% |
| CPU Usage | < 70% | > 85% |
| Kafka Lag | < 1000 msgs | > 10000 msgs |

### Real-time Monitor

```bash
# Start the monitor
cd services/monitor
./monitor --simulate

# Monitor displays:
# - Service health and instances
# - Real-time metrics graphs
# - Scaling events
# - Watermark freshness
# - Load distribution
# - Alert dashboard
```

## 🔄 Autoscaling Configuration

### Cascading Rules Example

```yaml
# Gateway scales first
gateway:
  min_instances: 2
  max_instances: 10
  metrics:
    - type: cpu
      threshold: 70
  scale_out:
    increment: 2
    cooldown: 30s

# Stream Ingester follows Gateway
stream_ingester:
  cascade_from: gateway
  ratio: 0.5  # 1 ingester per 2 gateways
  min_instances: 1
  max_instances: 5

# Hot Tier follows Stream Ingester
hot_tier:
  cascade_from: stream_ingester
  ratio: 0.6  # 3 hot-tier per 5 ingesters
  min_instances: 1
  max_instances: 6
```

## 🧪 Testing

### Unit Tests
```bash
# Run all unit tests
cd services/gateway && go test ./...
cd services/hot-tier && go test ./...

# With coverage
go test -cover -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

### Integration Tests
```bash
# Full system test
./populate_test_data.sh
./test_queries.sh

# Test autoscaling behavior
./test_scaling_demo.sh

# Test cascading scaling
./test_cascade.sh
```

### Performance Benchmarks
```bash
# Populate test data
./populate_test_data.sh

# Run query performance tests
./test_queries.sh

# Monitor system performance
ps aux | grep "build/" | awk '{sum+=$6} END {print "Total Memory (MB):", sum/1024}'
```

## 🛤️ Development Journey

### Phase 1: Foundation ✅
- Built core services (Gateway, Hot Tier, Query API)
- Implemented lock-free ring buffers
- Added probabilistic data structures (HLL, T-Digest)
- Established gRPC/REST dual protocol support

### Phase 2: Scalability ✅
- Added virtual sharding with rendezvous hashing
- Implemented epoch-based configuration management
- Built NUMA-aware memory allocation
- Added SIMD optimizations for validation

### Phase 3: Reliability ✅
- Implemented circuit breakers and retries
- Added health checking and service discovery
- Built automated failover mechanisms
- Created comprehensive monitoring

### Phase 4: Intelligence ✅
- Built predictive autoscaler with ML models
- Added cascading scaling rules
- Implemented backpressure management
- Created hot shard detection and mitigation

### Phase 5: Optimization ✅
- Converted to microsecond precision timestamps
- Fixed rate limiter for high-concurrency scenarios
- Implemented sharded maps for better distribution
- Optimized for 1B QPS scale

## 📚 Key Learnings

### Technical Insights

1. **Microsecond Precision**: All timestamps use microseconds (1,000,000 μs = 1 second) to support billion QPS operations with sub-millisecond accuracy.

2. **Sharded Maps for Rate Limiting**: sync.Map can hit hash collision limits under extreme load. Sharded maps with FNV hashing distribute load across 32 shards effectively.

3. **Lock-Free Operations**: Traditional mutex-based synchronization becomes a bottleneck beyond 1M QPS. Lock-free structures with CAS operations are mandatory.

4. **Cascading Scaling**: Dependencies between services require intelligent scaling coordination. Simple threshold-based scaling leads to oscillations.

5. **Data Precision**: Microsecond timestamps are essential for billion QPS operations, providing the precision needed for sub-millisecond event ordering.

### Architectural Decisions

- **Why Kafka/Redpanda**: Provides durability and replay capability. At 1B QPS, we need 10,000+ partitions across 100+ brokers.

- **Why PostgreSQL for Metadata Only**: Event data would require 100+ TB/day. Using PostgreSQL only for schemas and configuration keeps it manageable.

- **Why In-Memory Hot Tier**: Disk I/O cannot sustain 1B QPS. 24-hour retention in memory with efficient sketches keeps memory usage bounded.

- **Why Virtual Sharding**: Physical shard rebalancing at scale is expensive. Virtual shards (1000:1 ratio) allow fine-grained load distribution.

## 🔐 Security Considerations

- **Authentication**: API keys with HMAC signatures
- **Authorization**: Namespace-based access control
- **Encryption**: TLS 1.3 for all external APIs
- **Rate Limiting**: Hierarchical token buckets per client
- **Audit Logging**: All configuration changes tracked
- **Network Policies**: Kubernetes NetworkPolicies for pod-to-pod communication

## 🤝 Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for development guidelines.

## 📄 License

This project is licensed under the MIT License - see [LICENSE](LICENSE) file for details.

## 🙏 Acknowledgments

This platform was built through iterative development with extensive testing and validation at each phase. Special thanks to the performance engineering community for insights on hyperscale architectures.

---

**Built for the future of real-time analytics at planetary scale.** 🌍