#!/usr/bin/env bash

# Copyright The ORAS Authors.
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
# http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
NAMESPACE="oras-functional-test"
REGISTRY_PORT="${FUNCTIONAL_TEST_REGISTRY_PORT:-5000}"
PID_FILE="/tmp/oras-functional-test-portforward.pid"

# Clean up any previous port-forward
if [ -f "$PID_FILE" ]; then
  kill "$(cat "$PID_FILE")" 2>/dev/null || true
  rm -f "$PID_FILE"
fi

# Apply Kubernetes manifests
kubectl apply -f "$SCRIPT_DIR/deploy/registry.yaml"

# Wait for the registry pod to be ready
echo "Waiting for registry deployment to be ready..."
kubectl -n "$NAMESPACE" rollout status deployment/registry --timeout=120s

# Start port-forward in the background
kubectl -n "$NAMESPACE" port-forward svc/registry "$REGISTRY_PORT:5000" &
PF_PID=$!
echo "$PF_PID" > "$PID_FILE"

# Wait for the port to be listening
echo "Waiting for registry to be accessible at localhost:$REGISTRY_PORT..."
for i in $(seq 1 30); do
  if curl -sf "http://localhost:$REGISTRY_PORT/v2/" >/dev/null 2>&1; then
    echo "Registry is ready at localhost:$REGISTRY_PORT"
    exit 0
  fi
  sleep 1
done

echo "ERROR: Registry did not become ready within 30 seconds" >&2
kill "$PF_PID" 2>/dev/null || true
rm -f "$PID_FILE"
exit 1
