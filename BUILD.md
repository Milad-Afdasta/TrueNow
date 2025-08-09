# Building the TrueNow Analytics Platform

## Quick Start

```bash
# Build all services
./build.sh all

# Or using Make
make build-all
```

All binaries will be created in the `build/` directory.

## Build System Overview

The platform uses a centralized build system that:
- Compiles all services into a single `build/` directory
- Strips debug symbols for production builds (smaller binaries)
- Supports individual service builds
- Provides consistent build flags across all services

## Directory Structure

```
flow/
├── build/                    # All compiled binaries (git-ignored)
│   ├── gateway              # 9.7MB
│   ├── control-plane        # 16MB
│   ├── hot-tier            # 14MB
│   ├── query-api           # 19MB
│   ├── monitor             # 6.4MB
│   ├── autoscaler          # 7.9MB
│   ├── stream-ingester     # 17MB
│   ├── watermark-service   # 18MB
│   └── rebalancer          # 16MB
├── services/                # Source code
├── Makefile.services       # Services build configuration
└── build.sh               # Convenience build script
```

## Build Commands

### Using the Build Script

```bash
# Build all services
./build.sh all

# Build a specific service
./build.sh service gateway
./build.sh service hot-tier

# Clean all binaries
./build.sh clean

# Show binary sizes
./build.sh sizes

# Test compilation (no output)
./build.sh test

# Create deployment package
./build.sh package
```

### Using Make Directly

```bash
# Build all services
make -f Makefile.services build-all

# Build specific service
make -f Makefile.services build-gateway
make -f Makefile.services build-hot-tier

# Clean build directory
make -f Makefile.services clean

# Show sizes
make -f Makefile.services sizes

# Force rebuild
make -f Makefile.services rebuild-gateway

# Development build (with debug symbols)
make -f Makefile.services dev-build-gateway

# Build and run
make -f Makefile.services run-gateway
```

## Build Flags

Production builds use:
- `CGO_ENABLED=0` - Static binaries without C dependencies
- `GOWORK=off` - Ignore workspace file for consistent builds
- `-ldflags="-s -w"` - Strip debug symbols for smaller binaries

## Service Build Details

| Service | Entry Point | Binary Size | Build Time |
|---------|------------|-------------|------------|
| gateway | cmd/main.go | 9.7MB | ~2s |
| control-plane | cmd/main.go | 16MB | ~3s |
| hot-tier | cmd/main.go | 14MB | ~2s |
| query-api | cmd/main.go | 19MB | ~3s |
| monitor | main.go | 6.4MB | ~1s |
| autoscaler | cmd/autoscaler/main.go | 7.9MB | ~2s |
| stream-ingester | cmd/main.go | 17MB | ~3s |
| watermark-service | cmd/main.go | 18MB | ~3s |
| rebalancer | cmd/main.go | 16MB | ~2s |

## Dependencies

Before building, ensure you have:
- Go 1.21 or later
- Make (for Makefile usage)
- ~200MB free disk space for binaries

## Troubleshooting

### Build Failures

If a service fails to build:

1. **Update dependencies**:
   ```bash
   cd services/[service-name]
   GOWORK=off go mod tidy
   ```

2. **Check Go version**:
   ```bash
   go version  # Should be 1.21+
   ```

3. **Clear module cache**:
   ```bash
   go clean -modcache
   ```

### Missing Dependencies

Run the dependency sync script:
```bash
cd scripts
./sync-dependencies.sh
```

### Permission Denied

Make build script executable:
```bash
chmod +x build.sh
```

## CI/CD Integration

For CI/CD pipelines:

```bash
# Install dependencies
go mod download

# Build all services
make -f Makefile.services build-all

# Run tests
make -f Makefile.services test-build

# Package for deployment
make -f Makefile.services package
```

## Docker Build

To build in Docker (example):

```dockerfile
FROM golang:1.21-alpine AS builder
WORKDIR /app
COPY . .
RUN apk add --no-cache make
RUN make -f Makefile.services build-all

FROM alpine:latest
COPY --from=builder /app/build/ /usr/local/bin/
```

## Development Builds

For development with debug symbols:

```bash
# Build with debug symbols
make -f Makefile.services dev-build-gateway

# Output: build/gateway-debug
```

## Deployment Package

Create a tarball with all binaries:

```bash
make -f Makefile.services package
# Creates: flow-services-YYYYMMDD.tar.gz
```

## Clean Builds

To ensure clean builds:

```bash
# Clean and rebuild everything
make -f Makefile.services clean
make -f Makefile.services build-all
```

## Notes

- The `build/` directory is git-ignored
- Each binary is statically linked (no external dependencies)
- Production builds are optimized for size
- All services use consistent versioning from `versions.mk`