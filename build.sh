#!/bin/bash
# TrueNow Analytics Platform - Build Script
# Convenience script for building services

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Parse command line arguments
COMMAND=${1:-help}
SERVICE=${2:-}

# Show banner
show_banner() {
    echo -e "${BLUE}"
    echo "╔══════════════════════════════════════════════╗"
    echo "║     TrueNow Analytics Platform Builder      ║"
    echo "╚══════════════════════════════════════════════╝"
    echo -e "${NC}"
}

# Show help
show_help() {
    echo "Usage: ./build.sh [command] [service]"
    echo ""
    echo "Commands:"
    echo "  all       - Build all services"
    echo "  clean     - Clean all built binaries"
    echo "  service   - Build a specific service (requires service name)"
    echo "  test      - Test compilation of all services"
    echo "  sizes     - Show binary sizes"
    echo "  package   - Create deployment package"
    echo "  help      - Show this help message"
    echo ""
    echo "Services:"
    echo "  gateway, control-plane, hot-tier, query-api, monitor,"
    echo "  autoscaler, stream-ingester, watermark-service, rebalancer"
    echo ""
    echo "Examples:"
    echo "  ./build.sh all                    # Build all services"
    echo "  ./build.sh service gateway        # Build only gateway"
    echo "  ./build.sh clean                  # Clean build directory"
}

# Execute based on command
case $COMMAND in
    all)
        show_banner
        echo -e "${GREEN}Building all services...${NC}"
        make -f Makefile.services build-all
        ;;
    
    clean)
        show_banner
        echo -e "${YELLOW}Cleaning build directory...${NC}"
        make -f Makefile.services clean
        ;;
    
    service)
        if [ -z "$SERVICE" ]; then
            echo -e "${RED}Error: Service name required${NC}"
            echo "Usage: ./build.sh service [service-name]"
            exit 1
        fi
        show_banner
        echo -e "${GREEN}Building $SERVICE...${NC}"
        make -f Makefile.services build-$SERVICE
        ;;
    
    test)
        show_banner
        echo -e "${BLUE}Testing compilation...${NC}"
        make -f Makefile.services test-build
        ;;
    
    sizes)
        show_banner
        make -f Makefile.services sizes
        ;;
    
    package)
        show_banner
        echo -e "${GREEN}Creating deployment package...${NC}"
        make -f Makefile.services package
        ;;
    
    help|*)
        show_banner
        show_help
        ;;
esac