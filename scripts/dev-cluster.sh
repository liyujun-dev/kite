#!/usr/bin/env bash

# Managed by `mise daemons` (see [daemons.kite-cluster] in mise.toml).
#
# Starts the local k3d cluster on demand, stays in the foreground so pitchfork
# can supervise it, and stops the cluster when the daemon is stopped.
# Cluster data is kept between stop/start; remove it with:
#   k3d cluster delete kite-dev

set -euo pipefail

CLUSTER="${KITE_CLUSTER_NAME:-kite-dev}"
stopped=0

cleanup() {
    if [ "$stopped" -eq 1 ]; then
        return
    fi
    stopped=1
    echo "🛑 Stopping k3d cluster ${CLUSTER}..."
    k3d cluster stop "${CLUSTER}" >/dev/null 2>&1 || true
}
trap 'cleanup; exit 0' INT TERM
trap cleanup EXIT

if [ -n "$(k3d cluster list --no-headers "${CLUSTER}" 2>/dev/null)" ]; then
    echo "▶️  Starting existing k3d cluster ${CLUSTER}..."
    # Already-running clusters may make `start` fail; readiness below catches
    # any real problem.
    k3d cluster start "${CLUSTER}" --wait --timeout 180s || true
else
    echo "🔄 Creating k3d cluster ${CLUSTER}..."
    k3d cluster create "${CLUSTER}" --wait --timeout 180s
fi

echo "✅ k3d cluster ${CLUSTER} is ready"
echo "   context: k3d-${CLUSTER}"

# Keep the daemon alive; pitchfork stops it with SIGTERM.
while true; do
    sleep 3600 &
    wait $!
done
