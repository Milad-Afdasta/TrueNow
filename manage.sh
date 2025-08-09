#!/bin/bash
# TrueNow Analytics Platform - Management Script
# One-stop script for managing the entire platform

set -e

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
MAGENTA='\033[0;35m'
NC='\033[0m'

# Show banner
show_banner() {
    echo -e "${BLUE}"
    echo "╔══════════════════════════════════════════════════════╗"
    echo "║       TrueNow Analytics Platform Manager v1.0       ║"
    echo "╚══════════════════════════════════════════════════════╝"
    echo -e "${NC}"
}

# Show help
show_help() {
    show_banner
    echo "Usage: ./manage.sh [command] [options]"
    echo
    echo -e "${CYAN}Infrastructure Commands:${NC}"
    echo "  deps-start      - Start PostgreSQL, Redis, and Redpanda"
    echo "  deps-stop       - Stop infrastructure dependencies"
    echo "  deps-status     - Show status of dependencies"
    echo "  deps-clean      - Stop and remove all dependency data"
    echo
    echo -e "${CYAN}Build Commands:${NC}"
    echo "  build           - Build all services"
    echo "  build [service] - Build specific service"
    echo "  clean-build     - Clean and rebuild all services"
    echo
    echo -e "${CYAN}Service Commands:${NC}"
    echo "  start           - Start all services"
    echo "  stop            - Stop all services"
    echo "  restart         - Restart all services"
    echo "  status          - Show service status"
    echo "  logs [service]  - Show service logs"
    echo
    echo -e "${CYAN}Database Commands:${NC}"
    echo "  db-migrate      - Run database migrations"
    echo "  db-reset        - Reset database (WARNING: destroys data)"
    echo "  db-console      - Open PostgreSQL console"
    echo
    echo -e "${CYAN}Full Stack Commands:${NC}"
    echo "  up              - Start everything (deps + services)"
    echo "  down            - Stop everything"
    echo "  reset           - Full reset and restart"
    echo
    echo -e "${CYAN}Development Commands:${NC}"
    echo "  dev             - Start in development mode"
    echo "  test            - Run tests"
    echo "  monitor         - Open system monitor"
    echo
    echo -e "${CYAN}Examples:${NC}"
    echo "  ./manage.sh up                    # Start entire platform"
    echo "  ./manage.sh deps-start            # Start only dependencies"
    echo "  ./manage.sh build                 # Build all services"
    echo "  ./manage.sh start                 # Start all services"
    echo "  ./manage.sh logs gateway          # View gateway logs"
    echo "  ./manage.sh down                  # Stop everything"
}

# Check command availability
check_command() {
    if ! command -v $1 &> /dev/null; then
        echo -e "${RED}✗ $1 is not installed${NC}"
        return 1
    fi
    return 0
}

# Check all requirements
check_requirements() {
    echo -e "${CYAN}Checking requirements...${NC}"
    local failed=0
    
    check_command "docker" || failed=1
    check_command "docker-compose" || failed=1
    check_command "go" || failed=1
    check_command "make" || failed=1
    
    if [ $failed -eq 1 ]; then
        echo -e "${RED}Some requirements are missing. Please install them first.${NC}"
        exit 1
    fi
    
    echo -e "${GREEN}✓ All requirements met${NC}"
}

# Infrastructure dependencies
deps_start() {
    echo -e "${CYAN}Starting infrastructure dependencies...${NC}"
    docker-compose -f docker-compose.deps.yml up -d
    echo -e "${GREEN}✓ Dependencies started${NC}"
    
    echo -e "${CYAN}Waiting for services to be ready...${NC}"
    sleep 5
    
    deps_status
}

deps_stop() {
    echo -e "${CYAN}Stopping infrastructure dependencies...${NC}"
    docker-compose -f docker-compose.deps.yml stop
    echo -e "${GREEN}✓ Dependencies stopped${NC}"
}

deps_status() {
    echo -e "${CYAN}Infrastructure Status:${NC}"
    echo "═══════════════════════════════════════"
    
    # PostgreSQL
    if docker ps | grep -q flow-postgres; then
        echo -e "PostgreSQL:    ${GREEN}Running${NC} (port 5432)"
    else
        echo -e "PostgreSQL:    ${RED}Stopped${NC}"
    fi
    
    # Redis
    if docker ps | grep -q flow-redis; then
        echo -e "Redis:         ${GREEN}Running${NC} (port 6380)"
    else
        echo -e "Redis:         ${RED}Stopped${NC}"
    fi
    
    # Redpanda
    if docker ps | grep -q flow-redpanda; then
        echo -e "Redpanda:      ${GREEN}Running${NC} (port 9092)"
    else
        echo -e "Redpanda:      ${RED}Stopped${NC}"
    fi
    
    # Redpanda Console
    if docker ps | grep -q flow-redpanda-console; then
        echo -e "Console:       ${GREEN}Running${NC} (http://localhost:8090)"
    else
        echo -e "Console:       ${RED}Stopped${NC}"
    fi
    echo "═══════════════════════════════════════"
}

deps_clean() {
    echo -e "${YELLOW}⚠ WARNING: This will delete all data!${NC}"
    read -p "Are you sure? (y/N) " -n 1 -r
    echo
    if [[ $REPLY =~ ^[Yy]$ ]]; then
        docker-compose -f docker-compose.deps.yml down -v
        echo -e "${GREEN}✓ Dependencies cleaned${NC}"
    else
        echo "Cancelled"
    fi
}

# Database operations
db_migrate() {
    echo -e "${CYAN}Running database migrations...${NC}"
    
    # Wait for PostgreSQL to be ready
    echo "Waiting for PostgreSQL..."
    until PGPASSWORD=analytics123 psql -h localhost -U analytics -d analytics -c '\q' 2>/dev/null; do
        sleep 1
    done
    
    # Run migrations
    PGPASSWORD=analytics123 psql -h localhost -U analytics -d analytics \
        -f services/control-plane/migrations/001_initial_schema.up.sql 2>&1 | grep -v "already exists" | grep -v "must be owner" | grep -v "permission denied" || true
    
    echo -e "${GREEN}✓ Migrations completed${NC}"
}

db_reset() {
    echo -e "${YELLOW}⚠ WARNING: This will delete all database data!${NC}"
    read -p "Are you sure? (y/N) " -n 1 -r
    echo
    if [[ $REPLY =~ ^[Yy]$ ]]; then
        # Drop and recreate database
        PGPASSWORD=analytics123 psql -h localhost -U analytics -d postgres -c "DROP DATABASE IF EXISTS analytics;"
        PGPASSWORD=analytics123 psql -h localhost -U analytics -d postgres -c "CREATE DATABASE analytics;"
        db_migrate
        echo -e "${GREEN}✓ Database reset${NC}"
    else
        echo "Cancelled"
    fi
}

db_console() {
    echo -e "${CYAN}Opening PostgreSQL console...${NC}"
    PGPASSWORD=analytics123 psql -h localhost -U analytics -d analytics
}

# Build operations
build_services() {
    if [ -z "$1" ]; then
        echo -e "${CYAN}Building all services...${NC}"
        ./build.sh all
    else
        echo -e "${CYAN}Building $1...${NC}"
        ./build.sh service $1
    fi
}

clean_build() {
    echo -e "${CYAN}Clean building all services...${NC}"
    ./build.sh clean
    ./build.sh all
}

# Service operations
start_services() {
    echo -e "${CYAN}Starting services...${NC}"
    ./start-services.sh start
}

stop_services() {
    echo -e "${CYAN}Stopping services...${NC}"
    ./start-services.sh stop
}

restart_services() {
    echo -e "${CYAN}Restarting services...${NC}"
    ./start-services.sh restart
}

service_status() {
    ./start-services.sh status
}

service_logs() {
    if [ -z "$1" ]; then
        echo -e "${RED}Please specify a service name${NC}"
        echo "Usage: ./manage.sh logs [service]"
        exit 1
    fi
    ./start-services.sh logs $1
}

# Full stack operations
full_up() {
    show_banner
    echo -e "${MAGENTA}Starting TrueNow Analytics Platform...${NC}"
    echo
    
    # Check requirements
    check_requirements
    echo
    
    # Start dependencies
    deps_start
    echo
    
    # Run migrations
    db_migrate
    echo
    
    # Build services if needed
    if [ ! -d "build" ] || [ -z "$(ls -A build 2>/dev/null)" ]; then
        echo -e "${CYAN}No binaries found, building services...${NC}"
        build_services
        echo
    fi
    
    # Start services (with special handling for autoscaler)
    # The start-services.sh will handle most services
    # We'll ensure autoscaler runs in dry-run mode in the startup script
    start_services
    echo
    
    echo -e "${GREEN}════════════════════════════════════════════════${NC}"
    echo -e "${GREEN}Platform is ready!${NC}"
    echo -e "${GREEN}════════════════════════════════════════════════${NC}"
    echo
    echo -e "${CYAN}Access points:${NC}"
    echo "  Gateway API:        http://localhost:8088"
    echo "  Query API:          http://localhost:8081"
    echo "  Control Plane:      http://localhost:8001"
    echo "  Redpanda Console:   http://localhost:8090"
    echo
    echo -e "${CYAN}Monitor (run in separate terminal):${NC}"
    echo "  ./build/monitor"
    echo
    echo -e "${CYAN}Stop everything:${NC}"
    echo "  ./manage.sh down"
}

full_down() {
    show_banner
    echo -e "${MAGENTA}Stopping TrueNow Analytics Platform...${NC}"
    echo
    
    # Stop services
    stop_services
    echo
    
    # Stop dependencies
    deps_stop
    echo
    
    echo -e "${GREEN}Platform stopped${NC}"
}

full_reset() {
    echo -e "${YELLOW}⚠ WARNING: This will reset everything!${NC}"
    read -p "Are you sure? (y/N) " -n 1 -r
    echo
    if [[ $REPLY =~ ^[Yy]$ ]]; then
        full_down
        deps_clean
        ./build.sh clean
        rm -rf logs/ pids/
        echo -e "${GREEN}✓ Full reset complete${NC}"
    else
        echo "Cancelled"
    fi
}

# Development mode
dev_mode() {
    show_banner
    echo -e "${MAGENTA}Starting in development mode...${NC}"
    echo
    
    # Start dependencies
    deps_start
    echo
    
    # Run migrations
    db_migrate
    echo
    
    # Build with debug symbols
    echo -e "${CYAN}Building services with debug symbols...${NC}"
    make -f Makefile.services clean
    for service in gateway control-plane hot-tier query-api stream-ingester watermark-service rebalancer autoscaler; do
        make -f Makefile.services dev-build-$service
    done
    echo
    
    echo -e "${GREEN}Development environment ready${NC}"
    echo
    echo -e "${CYAN}Start services individually for debugging:${NC}"
    echo "  ./build/gateway-debug"
    echo "  ./build/control-plane-debug"
    echo "  etc..."
}

# Open monitor
open_monitor() {
    if [ ! -f "build/monitor" ]; then
        echo -e "${CYAN}Building monitor...${NC}"
        ./build.sh service monitor
    fi
    
    echo -e "${CYAN}Starting monitor...${NC}"
    ./build/monitor
}

# Main command handling
COMMAND=${1:-help}
shift || true

case $COMMAND in
    # Infrastructure
    deps-start)
        deps_start
        ;;
    deps-stop)
        deps_stop
        ;;
    deps-status)
        deps_status
        ;;
    deps-clean)
        deps_clean
        ;;
    
    # Database
    db-migrate)
        db_migrate
        ;;
    db-reset)
        db_reset
        ;;
    db-console)
        db_console
        ;;
    
    # Build
    build)
        build_services "$@"
        ;;
    clean-build)
        clean_build
        ;;
    
    # Services
    start)
        start_services
        ;;
    stop)
        stop_services
        ;;
    restart)
        restart_services
        ;;
    status)
        service_status
        ;;
    logs)
        service_logs "$@"
        ;;
    
    # Full stack
    up)
        full_up
        ;;
    down)
        full_down
        ;;
    reset)
        full_reset
        ;;
    
    # Development
    dev)
        dev_mode
        ;;
    monitor)
        open_monitor
        ;;
    
    # Help
    help|*)
        show_help
        ;;
esac