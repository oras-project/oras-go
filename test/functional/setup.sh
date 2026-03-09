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
MIRROR_PORT="${FUNCTIONAL_TEST_MIRROR_PORT:-5001}"
CERT_DIR="/tmp/oras-functional-certs"
CERTS_D="${CERT_DIR}/certs.d"
ENV_FILE="/tmp/oras-functional-env"
PID_FILE="/tmp/oras-functional-portforward.pid"
MIRROR_HOST="localhost:${MIRROR_PORT}"

# ── Clean up previous run ─────────────────────────────────────────────────────

if [ -f "${PID_FILE}" ]; then
  while IFS= read -r pid; do
    kill "${pid}" 2>/dev/null || true
  done < "${PID_FILE}"
  rm -f "${PID_FILE}"
fi
rm -rf "${CERT_DIR}"
rm -f "${ENV_FILE}"

# ── Primary registry ──────────────────────────────────────────────────────────

echo "==> Applying primary registry manifest..."
kubectl apply -f "${SCRIPT_DIR}/deploy/registry.yaml"

# ── TLS certificate for mirror registry ──────────────────────────────────────

echo "==> Generating self-signed TLS certificate for mirror registry..."
mkdir -p "${CERT_DIR}"

openssl req -x509 -newkey rsa:2048 -nodes \
  -keyout "${CERT_DIR}/mirror.key" \
  -out    "${CERT_DIR}/mirror.crt" \
  -days 1 \
  -subj "/CN=localhost" \
  -addext "subjectAltName=IP:127.0.0.1,DNS:localhost" \
  2>/dev/null

# Place the CA cert into a containers-certs.d directory structure so the
# functional tests can discover it via config.LoadCertsDirFromPaths.
mkdir -p "${CERTS_D}/${MIRROR_HOST}"
cp "${CERT_DIR}/mirror.crt" "${CERTS_D}/${MIRROR_HOST}/ca.crt"

# Create the Kubernetes secret the mirror-registry pod mounts.
kubectl -n "${NAMESPACE}" delete secret mirror-tls --ignore-not-found
kubectl -n "${NAMESPACE}" create secret generic mirror-tls \
  --from-file=mirror.crt="${CERT_DIR}/mirror.crt" \
  --from-file=mirror.key="${CERT_DIR}/mirror.key"

# ── Mirror registry (TLS) ─────────────────────────────────────────────────────

echo "==> Applying mirror registry manifest..."
kubectl apply -f "${SCRIPT_DIR}/deploy/mirror-registry.yaml"

# ── Wait for deployments ──────────────────────────────────────────────────────

echo "==> Waiting for primary registry deployment..."
kubectl -n "${NAMESPACE}" rollout status deployment/registry --timeout=120s

echo "==> Waiting for mirror registry deployment..."
kubectl -n "${NAMESPACE}" rollout status deployment/mirror-registry --timeout=120s

# ── Port-forward both registries ─────────────────────────────────────────────

kubectl -n "${NAMESPACE}" port-forward svc/registry "${REGISTRY_PORT}:5000" &
echo $! >> "${PID_FILE}"

kubectl -n "${NAMESPACE}" port-forward svc/mirror-registry "${MIRROR_PORT}:5443" &
echo $! >> "${PID_FILE}"

# ── Wait for registries to be accessible ─────────────────────────────────────

echo "==> Waiting for primary registry at localhost:${REGISTRY_PORT}..."
for i in $(seq 1 30); do
  if curl -sf "http://localhost:${REGISTRY_PORT}/v2/" >/dev/null 2>&1; then
    echo "    Primary registry is ready."
    break
  fi
  if [ "${i}" -eq 30 ]; then
    echo "ERROR: primary registry did not become ready." >&2
    exit 1
  fi
  sleep 1
done

echo "==> Waiting for mirror registry at ${MIRROR_HOST} (TLS)..."
for i in $(seq 1 30); do
  if curl -sf --cacert "${CERT_DIR}/mirror.crt" "https://${MIRROR_HOST}/v2/" >/dev/null 2>&1; then
    echo "    Mirror registry is ready."
    break
  fi
  if [ "${i}" -eq 30 ]; then
    echo "ERROR: mirror registry did not become ready." >&2
    exit 1
  fi
  sleep 1
done

# ── Write environment file for the Makefile to load ──────────────────────────

cat > "${ENV_FILE}" <<EOF
FUNCTIONAL_TEST_REGISTRY=localhost:${REGISTRY_PORT}
FUNCTIONAL_TEST_MIRROR_REGISTRY=${MIRROR_HOST}
FUNCTIONAL_TEST_CERTS_DIR=${CERTS_D}
EOF

echo "==> Setup complete."
echo "    Primary  : http://localhost:${REGISTRY_PORT}"
echo "    Mirror   : https://${MIRROR_HOST}  (CA cert in ${CERTS_D}/${MIRROR_HOST}/ca.crt)"
echo "    Env file : ${ENV_FILE}"
