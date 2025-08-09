# TrueNow Analytics Platform - Centralized Dependency Versions
# This file contains all external dependency versions used across the platform
# All services MUST use these versions to ensure consistency

# Core Go Dependencies
GO_VERSION := 1.21
GRPC_VERSION := v1.65.0
PROTOBUF_VERSION := v1.34.1

# Logging & Monitoring
LOGRUS_VERSION := v1.9.3
PROMETHEUS_CLIENT_VERSION := v1.18.0

# Database & Storage
POSTGRES_VERSION := v1.10.9
REDIS_VERSION := v9.12.0
KAFKA_VERSION := v0.4.48

# Web Frameworks
GORILLA_MUX_VERSION := v1.8.1
FASTHTTP_VERSION := v1.64.0
TCELL_VERSION := v2.6.0
TVIEW_VERSION := v0.0.0-20230826224341-9754ab44dc1c

# Configuration
VIPER_VERSION := v1.20.0-alpha.6

# Testing
TESTIFY_VERSION := v1.9.0

# Utilities
XXHASH_VERSION := v2.3.0
UUID_VERSION := v1.3.1

# Golang.org/x packages
GOLANG_SYS_VERSION := v0.25.0
GOLANG_NET_VERSION := v0.29.0
GOLANG_TEXT_VERSION := v0.18.0
GOLANG_CRYPTO_VERSION := v0.38.0
GOLANG_TERM_VERSION := v0.11.0
GOLANG_MOD_VERSION := v0.17.0
GOLANG_TOOLS_VERSION := v0.6.0

# Google Cloud & gRPC ecosystem
GENPROTO_VERSION := v0.0.0-20230822172742-b8732ec3820d
GOLANG_PROTOBUF_VERSION := v1.5.3
GOOGLE_CMP_VERSION := v0.5.9