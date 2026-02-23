#!/usr/bin/env bash
set -euo pipefail

PID_FILE="/tmp/oras-functional-test-portforward.pid"

# Kill port-forward
if [ -f "$PID_FILE" ]; then
  kill "$(cat "$PID_FILE")" 2>/dev/null || true
  rm -f "$PID_FILE"
fi

# Delete namespace
kubectl delete namespace oras-functional-test --ignore-not-found
