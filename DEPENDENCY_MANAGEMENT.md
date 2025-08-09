# TrueNow Analytics Platform - Dependency Management

## 📍 Centralized Version Control

All dependency versions are centrally managed to ensure consistency across the entire platform.

### Key Files

1. **`versions.mk`** - Makefile with all dependency versions (for build scripts)
2. **`versions/versions.go`** - Go package with version constants (for Go code)
3. **`go.work`** - Go workspace file for multi-module management
4. **`scripts/sync-dependencies.sh`** - Automated dependency synchronization

## 📦 Core Dependencies

| Package | Version | Purpose |
|---------|---------|---------|
| **google.golang.org/grpc** | v1.59.0 | gRPC framework for service communication |
| **google.golang.org/protobuf** | v1.31.0 | Protocol buffers for serialization |
| **github.com/sirupsen/logrus** | v1.9.3 | Structured logging |
| **github.com/stretchr/testify** | v1.9.0 | Testing assertions |
| **github.com/cespare/xxhash/v2** | v2.3.0 | Fast hashing |

## 🔄 Synchronization Process

### Automatic Synchronization

Run the sync script to update all services:

```bash
cd scripts
./sync-dependencies.sh
```

### Manual Update

To update a specific dependency across all services:

```bash
# Update gRPC version
NEW_VERSION="v1.60.0"
for service in services/*/; do
  cd $service
  go get google.golang.org/grpc@$NEW_VERSION
  go mod tidy
done
```

### Verify Consistency

Check all services are using the same versions:

```bash
# Check gRPC versions
for dir in services/*/; do
  echo "$(basename $dir):"
  grep "google.golang.org/grpc" $dir/go.mod
done
```

## 📊 Service Dependencies

### Control Plane
- PostgreSQL driver (`github.com/lib/pq`)
- Prometheus client (`github.com/prometheus/client_golang`)
- HTTP router (`github.com/gorilla/mux`)
- Configuration (`github.com/spf13/viper`)

### Gateway
- Kafka client (`github.com/segmentio/kafka-go`)
- Fast HTTP (`github.com/valyala/fasthttp`)
- JSON parser (`github.com/valyala/fastjson`)

### Hot-Tier
- gRPC server
- Ring buffer implementation
- Sketch algorithms

### Query API
- Redis client (`github.com/redis/go-redis/v9`)
- HTTP router (`github.com/gorilla/mux`)
- gRPC client

### Monitor
- Terminal UI (`github.com/rivo/tview`)
- Terminal handling (`github.com/gdamore/tcell/v2`)

## 🛡️ Security Updates

### Checking for Updates

```bash
# Check for available updates
go list -u -m all

# Check for security vulnerabilities
go install golang.org/x/vuln/cmd/govulncheck@latest
govulncheck ./...
```

### Update Process

1. Update version in `versions.mk`
2. Update version in `versions/versions.go`
3. Run `scripts/sync-dependencies.sh`
4. Test all services
5. Commit changes

## 📋 Version Policy

### Major Updates
- Require full testing suite pass
- Performance benchmarking required
- Review by team lead

### Minor Updates
- Standard testing required
- Can be done quarterly

### Patch Updates
- Security patches applied immediately
- Bug fixes applied within sprint

## 🔍 Audit Commands

### List All Dependencies
```bash
go list -m all | sort | uniq
```

### Check Version Mismatches
```bash
for service in services/*/; do
  echo "=== $(basename $service) ==="
  cd $service && go list -m all | grep -E "grpc|protobuf|logrus"
done
```

### Generate Dependency Report
```bash
go mod graph | grep -v "github.com/Milad-Afdasta/TrueNow"
```

## 💡 Best Practices

1. **Never update dependencies directly in service go.mod files**
   - Always update centralized versions first
   - Use sync script to propagate changes

2. **Test after updates**
   - Run unit tests for each service
   - Run integration tests
   - Verify performance metrics

3. **Document breaking changes**
   - Update this file with migration notes
   - Notify team of API changes

4. **Regular maintenance**
   - Monthly security vulnerability scan
   - Quarterly dependency review
   - Annual major version planning

## 🚨 Troubleshooting

### Version Conflicts
```bash
# Clear module cache
go clean -modcache

# Re-download dependencies
go mod download
```

### Build Failures After Update
```bash
# Reset to known good state
git checkout -- go.mod go.sum
go mod tidy
```

### Workspace Issues
```bash
# Sync workspace
go work sync
```

## 📞 Support

For dependency issues or questions:
- Check `versions.mk` for authoritative versions
- Run `scripts/sync-dependencies.sh` to fix inconsistencies
- Repository: https://github.com/Milad-Afdasta/TrueNow

---

**Last Updated**: 2025-08-08
**Maintained By**: TrueNow Platform Team