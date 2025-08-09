#!/bin/bash

# Redpanda Setup Script
# This script sets up Redpanda topics for the real-time analytics platform

set -e

echo "🚀 Setting up Redpanda for Real-Time Analytics Platform"

# Configuration
KAFKA_BROKER="localhost:19092"
REPLICATION_FACTOR=1  # For testing, use 3 in production
NUM_PARTITIONS=16     # For testing, use 10000 in production

# Wait for Redpanda to be ready
echo "⏳ Waiting for Redpanda to be ready..."
for i in {1..30}; do
    if rpk cluster health --brokers $KAFKA_BROKER 2>/dev/null | grep -q "Healthy"; then
        echo "✅ Redpanda is ready!"
        break
    fi
    echo "Waiting... ($i/30)"
    sleep 2
done

# Create topics
echo "📝 Creating Kafka topics..."

# Main event ingestion topic
rpk topic create events \
    --brokers $KAFKA_BROKER \
    --partitions $NUM_PARTITIONS \
    --replicas $REPLICATION_FACTOR \
    --topic-config retention.ms=86400000 \
    --topic-config compression.type=lz4 \
    --topic-config segment.ms=300000 || echo "Topic 'events' already exists"

# Dead letter queue for failed events
rpk topic create events-dlq \
    --brokers $KAFKA_BROKER \
    --partitions 4 \
    --replicas $REPLICATION_FACTOR \
    --topic-config retention.ms=604800000 \
    --topic-config compression.type=lz4 || echo "Topic 'events-dlq' already exists"

# Aggregated data topic
rpk topic create aggregations \
    --brokers $KAFKA_BROKER \
    --partitions 8 \
    --replicas $REPLICATION_FACTOR \
    --topic-config retention.ms=86400000 \
    --topic-config compression.type=lz4 || echo "Topic 'aggregations' already exists"

# Watermark topic for tracking progress
rpk topic create watermarks \
    --brokers $KAFKA_BROKER \
    --partitions 1 \
    --replicas $REPLICATION_FACTOR \
    --topic-config retention.ms=86400000 \
    --topic-config cleanup.policy=compact || echo "Topic 'watermarks' already exists"

# List all topics
echo ""
echo "📋 Current topics:"
rpk topic list --brokers $KAFKA_BROKER

# Get topic details
echo ""
echo "📊 Topic details:"
rpk topic describe events --brokers $KAFKA_BROKER

echo ""
echo "✅ Redpanda setup complete!"
echo ""
echo "🔗 Access points:"
echo "  - Kafka API: localhost:19092"
echo "  - Schema Registry: localhost:18085"
echo "  - Admin API: localhost:19644"
echo "  - Console UI: http://localhost:8090"