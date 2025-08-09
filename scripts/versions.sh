#!/bin/bash
# TrueNow Analytics Platform - Centralized Dependency Versions
# This file contains all external dependency versions used across the platform
# All services MUST use these versions to ensure consistency

# Core Go Dependencies
export GO_VERSION="1.21"
export GRPC_VERSION="v1.65.0"  # Updated to support SupportPackageIsVersion9
export PROTOBUF_VERSION="v1.34.1"  # Required by gRPC v1.65.0

# Logging & Monitoring
export LOGRUS_VERSION="v1.9.3"
export PROMETHEUS_CLIENT_VERSION="v1.18.0"

# Database & Storage
export POSTGRES_VERSION="v1.10.9"
export REDIS_VERSION="v9.12.0"
export KAFKA_VERSION="v0.4.48"

# Web Frameworks
export GORILLA_MUX_VERSION="v1.8.1"
export FASTHTTP_VERSION="v1.64.0"
export TCELL_VERSION="v2.6.0"
export TVIEW_VERSION="v0.0.0-20230826224341-9754ab44dc1c"

# Configuration
export VIPER_VERSION="v1.20.0-alpha.6"

# Testing
export TESTIFY_VERSION="v1.9.0"

# Utilities
export XXHASH_VERSION="v2.3.0"
export UUID_VERSION="v1.3.1"

# Golang.org/x packages
export GOLANG_SYS_VERSION="v0.25.0"
export GOLANG_NET_VERSION="v0.29.0"
export GOLANG_TEXT_VERSION="v0.18.0"
export GOLANG_CRYPTO_VERSION="v0.38.0"
export GOLANG_TERM_VERSION="v0.11.0"
export GOLANG_MOD_VERSION="v0.17.0"
export GOLANG_TOOLS_VERSION="v0.6.0"

# Google Cloud & gRPC ecosystem
export GENPROTO_VERSION="v0.0.0-20230822172742-b8732ec3820d"
export GOLANG_PROTOBUF_VERSION="v1.5.3"
export GOOGLE_CMP_VERSION="v0.5.9"