# TrueNow Analytics Platform Monitor

Professional Terminal User Interface (TUI) monitoring solution for the TrueNow real-time analytics platform, capable of handling 100M+ events/second.

## Features

### Interactive TUI Dashboard
- **Multi-panel Layout**: Service table, latency percentiles, throughput metrics, alerts, and resource views
- **Real-time Updates**: Automatic refresh every 2 seconds with pause/resume capability
- **Interactive Navigation**: Select services for detailed metrics, keyboard shortcuts for quick actions
- **Color-coded Metrics**: Visual indicators for health status, performance thresholds

### Comprehensive Metrics
- **Latency Percentiles**: P50, P90, P95, P99, P99.9, and Max latencies
- **Service Health**: Instance count, health percentage, CPU, memory, QPS, error rates
- **Throughput Tracking**: Platform-wide and per-service events/sec, queries/sec, bytes/sec
- **Resource Monitoring**: System-wide CPU cores, memory, disk utilization
- **Custom Service Metrics**: Service-specific metrics like buffer usage, dedup ratios, cache hit rates

## Usage

```bash
# Run in live mode (connects to actual services)
./monitor

# Run in simulation mode (uses mock data for demonstration)
./monitor --simulate

# Show help
./monitor --help
```

### Requirements
- **Terminal Environment**: The monitor requires a proper TTY/terminal to display the TUI
- **Terminal Size**: Recommended minimum 120x40 characters for optimal display
- **Color Support**: Terminal with 256-color support recommended

### Keyboard Shortcuts
- **Navigation**:
  - `↑/↓` - Navigate through services
  - `Tab` - Switch between panels
  - `Enter` - Select service for detailed view
- **Actions**:
  - `r` - Manually refresh data
  - `p` - Pause/resume auto-refresh
  - `q` or `Esc` - Quit application
  - `F1` - Show help screen

## Service Coverage

The monitor tracks all TrueNow platform services with their instance counts:
- **Control Plane** (1 instance) - Metadata and schema management
- **Gateway** (3 instances) - Ingestion endpoint with validation
- **Hot Tier** (8 instances) - In-memory aggregation with ring buffers
- **Query API** (4 instances) - Query planning and routing
- **Stream Ingester** (6 instances) - Kafka consumer and transformation
- **Watermark Service** (2 instances) - Freshness tracking
- **Rebalancer** (2 instances) - Shard management
- **Autoscaler** (1 instance) - Dynamic scaling control

## TUI Layout

```
╔═══════════════════════════════════════════════════════════════════╗
║                 TrueNow Analytics Platform Monitor                 ║
╚═══════════════════════════════════════════════════════════════════╝

┌─────────────── Service Status Overview ────────────────────────────┐
│ Service    Status  Health  CPU%  Mem%  QPS   P50   P95   P99  Err%│
│ gateway    healthy  100%   45.2  62.1  125K  5.2   15.3  25.1 0.02│
│ hot-tier   healthy  100%   72.3  78.4  450K  2.1   5.2   10.3 0.01│
└─────────────────────────────────────────────────────────────────────┘

┌─ Latency Percentiles ─┐ ┌─ Throughput Metrics ─┐ ┌─ System Resources ─┐
│ Service  P50  P99  Max│ │ Events/s:    580K    │ │ Health:     95%    │
│ Gateway  5.2  25.1 125│ │ Queries/s:   5.2K    │ │ CPU Cores:  256    │
└───────────────────────┘ └──────────────────────┘ └────────────────────┘

┌─ Active Alerts ───────┐ ┌─────────── Service Details ────────────────────┐
│ ⚠ High CPU usage     │ │ Select a service above for detailed metrics     │
└───────────────────────┘ └──────────────────────────────────────────────────┘
```

## Color Scheme

Optimized for black terminal backgrounds with light, high-contrast colors:
- 🟢 **Light Green** - Healthy services and good performance
- 🟡 **Yellow** - Warnings and degraded performance  
- 🔵 **Light Cyan** - Headers and labels
- ⚪ **White** - Primary text and values
- 🟣 **Light Pink/Coral** - Critical issues (replaced dark red for visibility)
- 🔶 **Light Salmon/Orange** - High percentiles (P99)

## Simulation Mode

In simulation mode (`--simulate`), the monitor generates realistic mock data:
- Service health varies with most services at 100% health
- Metrics include slight variations to simulate real-time changes
- One service (watermark) shows partial health (50%) for demonstration
- Alerts are generated based on threshold violations

## Build

```bash
# Build the monitor
go build -o monitor main.go

# With Go workspace disabled (if using go.work file)
GOWORK=off go build -o monitor main.go
```

## Architecture

The monitor consists of:
- **Main TUI Application** (`main.go`) - Terminal UI with tview/tcell
- **Collector** (`internal/collector/`) - Metrics collection from services
- **UI Components** (`internal/ui/`) - Color schemes and UI helpers

### Key Dependencies
- `github.com/rivo/tview` - Terminal UI framework
- `github.com/gdamore/tcell/v2` - Terminal control library

## Troubleshooting

### "open /dev/tty: device not configured"
The monitor requires a proper terminal (TTY) to run. This error occurs when:
- Running in a non-interactive environment
- Running through SSH without proper TTY allocation
- Running in CI/CD pipelines

Solution: Run the monitor in an interactive terminal session.

### Services showing as unhealthy
In simulation mode, the monitor generates mock data. In live mode, ensure:
- Services are actually running on expected ports
- Network connectivity to service endpoints
- Health check endpoints are accessible