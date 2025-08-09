#!/bin/bash

# Run Redpanda locally without Docker
# For testing purposes only

set -e

echo "🚀 Starting Redpanda locally (without Docker)"
echo ""
echo "⚠️  Docker is not installed. Please install Docker Desktop from:"
echo "    https://www.docker.com/products/docker-desktop/"
echo ""
echo "Once Docker is installed, you can run:"
echo "  cd /Users/null/work/flow/infrastructure"
echo "  docker-compose up -d"
echo "  ./redpanda-setup.sh"
echo ""
echo "For now, let's test with a mock Kafka implementation..."

# Create a mock Kafka environment for testing
mkdir -p /tmp/redpanda-mock/{data,logs}

echo ""
echo "📝 Creating mock configuration..."
cat > /tmp/redpanda-mock/config.yml << EOF
# Mock Redpanda Configuration
brokers:
  - localhost:19092
topics:
  - name: events
    partitions: 16
    replication: 1
  - name: events-dlq
    partitions: 4
    replication: 1
  - name: aggregations
    partitions: 8
    replication: 1
  - name: watermarks
    partitions: 1
    replication: 1
EOF

echo "✅ Mock configuration created at /tmp/redpanda-mock/config.yml"
echo ""
echo "📋 Mock Topics:"
echo "  - events (16 partitions)"
echo "  - events-dlq (4 partitions)"
echo "  - aggregations (8 partitions)"
echo "  - watermarks (1 partition)"
echo ""
echo "⚠️  Note: This is a mock setup for testing without Kafka"
echo "    The gateway and stream-ingester services will run in test mode"