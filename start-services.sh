#!/bin/bash
# TrueNow Analytics Platform - Service Startup Script
# Starts all services in the correct dependency order

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m' # No Color

# Configuration
BUILD_DIR="build"
LOG_DIR="logs"
PID_DIR="pids"
STARTUP_DELAY=2  # Seconds between service starts
HEALTH_CHECK_RETRIES=10
HEALTH_CHECK_DELAY=1

# Function to get service port
get_service_port() {
    case $1 in
        "control-plane") echo "8001" ;;
        "gateway") echo "8088" ;;  # Actual gateway port
        "hot-tier") echo "9090" ;;
        "query-api") echo "8081" ;;
        "stream-ingester") echo "0" ;;  # No HTTP port, only Kafka consumer
        "watermark-service") echo "8084" ;;  # Actual HTTP port
        "rebalancer") echo "8086" ;;  # Actual HTTP port
        "autoscaler") echo "8095" ;;  # Actual HTTP API port
        "monitor") echo "0" ;;
        *) echo "0" ;;
    esac
}

# Service startup order (respecting dependencies)
SERVICE_ORDER=(
    "control-plane"      # Must start first (metadata store)
    "hot-tier"          # Core storage service
    "gateway"           # Ingestion entry point
    "stream-ingester"   # Consumes from Kafka
    "query-api"         # Depends on hot-tier
    "watermark-service" # Monitoring service
    "rebalancer"        # Load balancing service
    # "autoscaler"        # Scaling coordinator - DISABLED: port allocation issues
    # "monitor"           # UI monitoring (last) - DISABLED: not needed for headless operation
)

# Parse command line arguments
COMMAND=${1:-start}
SERVICE=${2:-all}

# Ensure required directories exist
setup_directories() {
    mkdir -p "$LOG_DIR" "$PID_DIR"
}

# Check if a service is already running
is_running() {
    local service=$1
    local pid_file="$PID_DIR/$service.pid"
    
    if [ -f "$pid_file" ]; then
        local pid=$(cat "$pid_file")
        if ps -p "$pid" > /dev/null 2>&1; then
            return 0
        else
            # Process died but PID file exists
            rm -f "$pid_file"
        fi
    fi
    return 1
}

# Check if a port is available
check_port() {
    local port=$1
    if [ "$port" == "0" ]; then
        return 0  # No port to check
    fi
    
    if lsof -i:$port > /dev/null 2>&1; then
        return 1  # Port is in use
    fi
    return 0  # Port is available
}

# Wait for a service to be healthy
wait_for_health() {
    local service=$1
    local port=$(get_service_port "$service")
    
    if [ "$port" == "0" ]; then
        return 0  # No health check for services without ports
    fi
    
    echo -n "  Waiting for $service to be healthy"
    
    for i in $(seq 1 $HEALTH_CHECK_RETRIES); do
        if nc -z localhost "$port" 2>/dev/null; then
            echo -e " ${GREEN}✓${NC}"
            return 0
        fi
        echo -n "."
        sleep $HEALTH_CHECK_DELAY
    done
    
    echo -e " ${RED}✗${NC}"
    return 1
}

# Start a single service
start_service() {
    local service=$1
    
    if is_running "$service"; then
        echo -e "${YELLOW}⚠${NC}  $service is already running"
        return 0
    fi
    
    # Check if binary exists
    if [ ! -f "$BUILD_DIR/$service" ]; then
        echo -e "${RED}✗${NC}  $service binary not found in $BUILD_DIR/"
        echo "    Run './build.sh all' to build services first"
        return 1
    fi
    
    # Check if port is available
    local port=$(get_service_port "$service")
    if [ "$port" != "0" ] && ! check_port "$port"; then
        echo -e "${RED}✗${NC}  Port $port for $service is already in use"
        return 1
    fi
    
    echo -e "${BLUE}→${NC}  Starting $service..."
    
    # Start the service
    local log_file="$LOG_DIR/$service.log"
    local pid_file="$PID_DIR/$service.pid"
    
    # Special handling for monitor (interactive terminal UI)
    if [ "$service" == "monitor" ]; then
        echo -e "${CYAN}ℹ${NC}  Monitor runs in terminal mode. Start it in a separate terminal:"
        echo "    ./$BUILD_DIR/monitor"
        return 0
    fi
    
    # Start service in background (with special handling for stream-ingester)
    if [ "$service" == "stream-ingester" ]; then
        KAFKA_BROKERS=localhost:19092 nohup "./$BUILD_DIR/$service" > "$log_file" 2>&1 &
    else
        nohup "./$BUILD_DIR/$service" > "$log_file" 2>&1 &
    fi
    local pid=$!
    echo $pid > "$pid_file"
    
    # Wait for service to be healthy (skip health check for stream-ingester)
    if [ "$service" == "stream-ingester" ]; then
        # Stream ingester doesn't expose a port, just check if process is running
        sleep 2
        if ps -p $pid > /dev/null 2>&1; then
            echo -e "${GREEN}✓${NC}  $service started successfully (PID: $pid, Kafka consumer)"
            return 0
        else
            echo -e "${RED}✗${NC}  $service failed to start"
            rm -f "$pid_file"
            return 1
        fi
    elif wait_for_health "$service"; then
        echo -e "${GREEN}✓${NC}  $service started successfully (PID: $pid, Port: $port)"
        return 0
    else
        echo -e "${RED}✗${NC}  $service failed to start properly"
        kill $pid 2>/dev/null || true
        rm -f "$pid_file"
        return 1
    fi
}

# Stop a single service
stop_service() {
    local service=$1
    local pid_file="$PID_DIR/$service.pid"
    
    if ! is_running "$service"; then
        echo -e "${YELLOW}⚠${NC}  $service is not running"
        return 0
    fi
    
    local pid=$(cat "$pid_file")
    echo -e "${BLUE}→${NC}  Stopping $service (PID: $pid)..."
    
    # Try graceful shutdown first
    kill -TERM "$pid" 2>/dev/null || true
    
    # Wait for process to stop
    local count=0
    while ps -p "$pid" > /dev/null 2>&1 && [ $count -lt 10 ]; do
        sleep 1
        count=$((count + 1))
    done
    
    # Force kill if still running
    if ps -p "$pid" > /dev/null 2>&1; then
        echo -e "${YELLOW}⚠${NC}  Force killing $service"
        kill -9 "$pid" 2>/dev/null || true
    fi
    
    rm -f "$pid_file"
    echo -e "${GREEN}✓${NC}  $service stopped"
}

# Start all services
start_all() {
    echo -e "${BLUE}╔══════════════════════════════════════════════╗${NC}"
    echo -e "${BLUE}║     Starting TrueNow Analytics Platform     ║${NC}"
    echo -e "${BLUE}╚══════════════════════════════════════════════╝${NC}"
    echo
    
    # Check prerequisites
    check_prerequisites || return 1
    
    echo -e "${GREEN}Starting services in dependency order...${NC}"
    echo
    
    local failed=0
    for service in "${SERVICE_ORDER[@]}"; do
        if ! start_service "$service"; then
            failed=1
            echo -e "${RED}Failed to start $service. Stopping startup sequence.${NC}"
            break
        fi
        
        # Add delay between service starts (except for monitor)
        if [ "$service" != "monitor" ]; then
            sleep $STARTUP_DELAY
        fi
    done
    
    echo
    if [ $failed -eq 0 ]; then
        echo -e "${GREEN}════════════════════════════════════════════════${NC}"
        echo -e "${GREEN}All services started successfully!${NC}"
        echo -e "${GREEN}════════════════════════════════════════════════${NC}"
        show_status
    else
        echo -e "${RED}════════════════════════════════════════════════${NC}"
        echo -e "${RED}Some services failed to start${NC}"
        echo -e "${RED}Check logs in $LOG_DIR/ for details${NC}"
        echo -e "${RED}════════════════════════════════════════════════${NC}"
        return 1
    fi
}

# Stop all services
stop_all() {
    echo -e "${BLUE}╔══════════════════════════════════════════════╗${NC}"
    echo -e "${BLUE}║     Stopping TrueNow Analytics Platform     ║${NC}"
    echo -e "${BLUE}╚══════════════════════════════════════════════╝${NC}"
    echo
    
    # Stop in reverse order
    for ((i=${#SERVICE_ORDER[@]}-1; i>=0; i--)); do
        service="${SERVICE_ORDER[$i]}"
        if [ "$service" != "monitor" ]; then
            stop_service "$service"
        fi
    done
    
    echo
    echo -e "${GREEN}All services stopped${NC}"
}

# Check prerequisites
check_prerequisites() {
    echo -e "${CYAN}Checking prerequisites...${NC}"
    
    local failed=0
    
    # Check if PostgreSQL is running
    if ! pg_isready -h localhost -p 5432 > /dev/null 2>&1; then
        echo -e "${RED}✗${NC}  PostgreSQL is not running on port 5432"
        echo "    Start PostgreSQL first: brew services start postgresql"
        failed=1
    else
        echo -e "${GREEN}✓${NC}  PostgreSQL is running"
    fi
    
    # Check if Redis is running (on port 6380 for Docker or 6379 for local)
    if ! redis-cli -p 6380 ping > /dev/null 2>&1 && ! redis-cli -p 6379 ping > /dev/null 2>&1; then
        echo -e "${RED}✗${NC}  Redis is not running on port 6380 (Docker) or 6379 (local)"
        echo "    Start Redis first: docker-compose -f docker-compose.deps.yml up -d"
        failed=1
    else
        echo -e "${GREEN}✓${NC}  Redis is running"
    fi
    
    # Check if Kafka/Redpanda is running
    if ! nc -z localhost 9092 2>/dev/null; then
        echo -e "${YELLOW}⚠${NC}  Kafka/Redpanda is not running on port 9092"
        echo "    Start Redpanda: docker-compose -f infrastructure/docker/docker-compose.yml up -d redpanda"
        # Don't fail for Kafka as it might be optional for some services
    else
        echo -e "${GREEN}✓${NC}  Kafka/Redpanda is running"
    fi
    
    # Check if all binaries exist
    echo -e "${CYAN}Checking service binaries...${NC}"
    for service in "${SERVICE_ORDER[@]}"; do
        if [ ! -f "$BUILD_DIR/$service" ]; then
            echo -e "${RED}✗${NC}  $service binary not found"
            failed=1
        else
            echo -e "${GREEN}✓${NC}  $service binary found"
        fi
    done
    
    echo
    if [ $failed -eq 1 ]; then
        return 1
    fi
    return 0
}

# Show status of all services
show_status() {
    echo
    echo -e "${CYAN}Service Status:${NC}"
    echo -e "${CYAN}═══════════════════════════════════════════════${NC}"
    printf "%-20s %-10s %-10s %-20s\n" "SERVICE" "STATUS" "PID" "PORT"
    echo -e "${CYAN}───────────────────────────────────────────────${NC}"
    
    for service in "${SERVICE_ORDER[@]}"; do
        local status="${RED}Stopped${NC}"
        local pid="-"
        local port=$(get_service_port "$service")
        
        if [ "$port" == "0" ]; then
            port="-"
        fi
        
        if is_running "$service"; then
            status="${GREEN}Running${NC}"
            pid=$(cat "$PID_DIR/$service.pid")
        fi
        
        printf "%-20s %-20b %-10s %-20s\n" "$service" "$status" "$pid" "$port"
    done
    echo -e "${CYAN}═══════════════════════════════════════════════${NC}"
}

# Show logs for a service
show_logs() {
    local service=$1
    local log_file="$LOG_DIR/$service.log"
    
    if [ ! -f "$log_file" ]; then
        echo -e "${RED}No log file found for $service${NC}"
        return 1
    fi
    
    echo -e "${CYAN}Showing last 50 lines of $service logs:${NC}"
    echo -e "${CYAN}═══════════════════════════════════════════════${NC}"
    tail -50 "$log_file"
}

# Show help
show_help() {
    echo "Usage: $0 [command] [service]"
    echo
    echo "Commands:"
    echo "  start [service]  - Start all services or a specific service"
    echo "  stop [service]   - Stop all services or a specific service"
    echo "  restart [service] - Restart all services or a specific service"
    echo "  status           - Show status of all services"
    echo "  logs [service]   - Show logs for a service"
    echo "  clean            - Clean up PID and log files"
    echo "  help             - Show this help message"
    echo
    echo "Services:"
    echo "  ${SERVICE_ORDER[@]}"
    echo
    echo "Examples:"
    echo "  $0 start                  # Start all services"
    echo "  $0 start gateway          # Start only gateway"
    echo "  $0 stop                   # Stop all services"
    echo "  $0 status                 # Show service status"
    echo "  $0 logs gateway           # Show gateway logs"
}

# Clean up PID and log files
clean_files() {
    echo -e "${YELLOW}Cleaning up PID and log files...${NC}"
    rm -rf "$PID_DIR"/*.pid
    rm -rf "$LOG_DIR"/*.log
    echo -e "${GREEN}✓${NC}  Cleanup complete"
}

# Main execution
setup_directories

case $COMMAND in
    start)
        if [ "$SERVICE" == "all" ] || [ -z "$SERVICE" ]; then
            start_all
        else
            start_service "$SERVICE"
        fi
        ;;
    
    stop)
        if [ "$SERVICE" == "all" ] || [ -z "$SERVICE" ]; then
            stop_all
        else
            stop_service "$SERVICE"
        fi
        ;;
    
    restart)
        if [ "$SERVICE" == "all" ] || [ -z "$SERVICE" ]; then
            stop_all
            echo
            sleep 2
            start_all
        else
            stop_service "$SERVICE"
            sleep 1
            start_service "$SERVICE"
        fi
        ;;
    
    status)
        show_status
        ;;
    
    logs)
        if [ -z "$SERVICE" ] || [ "$SERVICE" == "all" ]; then
            echo -e "${RED}Please specify a service name${NC}"
            echo "Usage: $0 logs [service]"
            exit 1
        fi
        show_logs "$SERVICE"
        ;;
    
    clean)
        clean_files
        ;;
    
    help|*)
        show_help
        ;;
esac