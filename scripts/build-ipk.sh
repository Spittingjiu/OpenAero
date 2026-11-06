#!/bin/bash
# ==============================================================================
# OpenAero - Automated Core Compilation & Packaging Script
# ==============================================================================
# Usage:
#   ./scripts/build-ipk.sh [all|arm64|amd64|mipsle|deploy]
#
# Environment variables:
#   ROUTER_IP   : Target router IP (default: 192.168.1.1)
#   ROUTER_PORT : Target SSH port (default: 22)
#   ROUTER_USER : Target SSH user (default: root)
# ==============================================================================

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
BIN_DIR="${ROOT_DIR}/bin"
CORE_DIR="${ROOT_DIR}/openaero-core"

TARGET="${1:-arm64}"
ROUTER_IP="${ROUTER_IP:-192.168.1.1}"
ROUTER_PORT="${ROUTER_PORT:-22}"
ROUTER_USER="${ROUTER_USER:-root}"

mkdir -p "${BIN_DIR}"

build_arm64() {
    echo "==> Building openaero-core for linux/arm64..."
    cd "${CORE_DIR}"
    CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -o "${BIN_DIR}/openaero-core-arm64" .
    echo "    Built: ${BIN_DIR}/openaero-core-arm64 ($(stat -c%s "${BIN_DIR}/openaero-core-arm64" 2>/dev/null || stat -f%z "${BIN_DIR}/openaero-core-arm64") bytes)"
}

build_amd64() {
    echo "==> Building openaero-core for linux/amd64 (x86_64)..."
    cd "${CORE_DIR}"
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o "${BIN_DIR}/openaero-core-amd64" .
    echo "    Built: ${BIN_DIR}/openaero-core-amd64 ($(stat -c%s "${BIN_DIR}/openaero-core-amd64" 2>/dev/null || stat -f%z "${BIN_DIR}/openaero-core-amd64") bytes)"
}

build_mipsle() {
    echo "==> Building openaero-core for linux/mipsle (MT7621 softfloat)..."
    cd "${CORE_DIR}"
    CGO_ENABLED=0 GOOS=linux GOARCH=mipsle GOMIPS=softfloat go build -ldflags="-s -w" -o "${BIN_DIR}/openaero-core-mipsle" .
    echo "    Built: ${BIN_DIR}/openaero-core-mipsle ($(stat -c%s "${BIN_DIR}/openaero-core-mipsle" 2>/dev/null || stat -f%z "${BIN_DIR}/openaero-core-mipsle") bytes)"
}

deploy_router() {
    build_arm64
    echo "==> Deploying to router ${ROUTER_USER}@${ROUTER_IP}:${ROUTER_PORT}..."
    scp -P "${ROUTER_PORT}" "${BIN_DIR}/openaero-core-arm64" "${ROUTER_USER}@${ROUTER_IP}:/tmp/openaero-core.new"
    ssh -p "${ROUTER_PORT}" "${ROUTER_USER}@${ROUTER_IP}" "chmod +x /tmp/openaero-core.new && mv /tmp/openaero-core.new /usr/bin/openaero-core && /etc/init.d/openaero restart"
    echo "==> Deployment completed successfully!"
}

case "${TARGET}" in
    arm64)
        build_arm64
        ;;
    amd64)
        build_amd64
        ;;
    mipsle)
        build_mipsle
        ;;
    all)
        build_arm64
        build_amd64
        build_mipsle
        ;;
    deploy)
        deploy_router
        ;;
    *)
        echo "Unknown target: ${TARGET}. Valid options: arm64, amd64, mipsle, all, deploy"
        exit 1
        ;;
esac

echo "==> All operations completed successfully."
