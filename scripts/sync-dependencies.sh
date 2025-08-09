#!/bin/bash
# TrueNow Analytics Platform - Dependency Synchronization Script
# This script ensures all services use the same dependency versions

set -e

# Source the versions
SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
source "$SCRIPT_DIR/versions.sh"

echo "🔄 TrueNow Dependency Synchronization"
echo "====================================="
echo

# Services to update
SERVICES=(
    "autoscaler"
    "control-plane"
    "gateway"
    "hot-tier"
    "monitor"
    "query-api"
    "rebalancer"
    "stream-ingester"
    "watermark-service"
)

# Update each service
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"
for service in "${SERVICES[@]}"; do
    echo "📦 Updating $service..."
    cd "$PROJECT_ROOT/services/$service"
    
    # Core dependencies
    go get google.golang.org/grpc@${GRPC_VERSION}
    go get google.golang.org/protobuf@${PROTOBUF_VERSION}
    
    # Common dependencies
    go get github.com/sirupsen/logrus@${LOGRUS_VERSION}
    go get github.com/stretchr/testify@${TESTIFY_VERSION}
    go get github.com/cespare/xxhash/v2@${XXHASH_VERSION}
    
    # Golang.org/x packages
    go get golang.org/x/sys@${GOLANG_SYS_VERSION}
    go get golang.org/x/net@${GOLANG_NET_VERSION}
    go get golang.org/x/text@${GOLANG_TEXT_VERSION}
    
    # Service-specific dependencies
    case $service in
        "control-plane")
            go get github.com/lib/pq@${POSTGRES_VERSION}
            go get github.com/prometheus/client_golang@${PROMETHEUS_CLIENT_VERSION}
            go get github.com/gorilla/mux@${GORILLA_MUX_VERSION}
            go get github.com/spf13/viper@${VIPER_VERSION}
            ;;
        "gateway")
            go get github.com/valyala/fasthttp@${FASTHTTP_VERSION}
            go get github.com/segmentio/kafka-go@${KAFKA_VERSION}
            go get github.com/valyala/fastjson@v1.6.4
            ;;
        "query-api")
            go get github.com/redis/go-redis/v9@${REDIS_VERSION}
            go get github.com/gorilla/mux@${GORILLA_MUX_VERSION}
            ;;
        "monitor")
            go get github.com/gdamore/tcell/v2@${TCELL_VERSION}
            go get github.com/rivo/tview@${TVIEW_VERSION}
            ;;
        "rebalancer")
            go get github.com/gorilla/mux@${GORILLA_MUX_VERSION}
            ;;
        "stream-ingester")
            go get github.com/segmentio/kafka-go@${KAFKA_VERSION}
            ;;
        "watermark-service")
            go get github.com/gorilla/mux@${GORILLA_MUX_VERSION}
            go get github.com/prometheus/client_golang@${PROMETHEUS_CLIENT_VERSION}
            ;;
        "hot-tier")
            # Hot tier doesn't need extra deps, just core ones
            ;;
        "autoscaler")
            # Autoscaler uses internal packages only
            ;;
    esac
    
    # Clean up
    go mod tidy
    
    echo "  ✅ $service updated"
    echo
done

echo "✅ All services synchronized!"
echo
echo "Version Summary:"
echo "  • gRPC:     ${GRPC_VERSION}"
echo "  • Protobuf: ${PROTOBUF_VERSION}"
echo "  • Logrus:   ${LOGRUS_VERSION}"
echo "  • Testify:  ${TESTIFY_VERSION}"