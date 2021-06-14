#!/bin/bash -ex

# Copyright The ORAS Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
cd $DIR/../

LOCAL_REGISTRY_HOSTNAME="${LOCAL_REGISTRY_HOSTNAME:-localhost}"

# Cleanup from previous runs
rm -f hello.txt
rm -f bin/oras-acceptance-* || true
docker rm -f oras-acceptance-registry || true

# Build the examples into binaries
CGO_ENABLED=0 go build -v -o bin/oras-acceptance-simple ./examples/simple
CGO_ENABLED=0 go build -v -o bin/oras-acceptance-advanced ./examples/advanced

# Run a test registry and expose at localhost:5000
trap "docker rm -f oras-acceptance-registry" EXIT
docker run -d -p 5000:5000 \
  --name oras-acceptance-registry \
  index.docker.io/registry

# Wait for a connection to port 5000 (timeout after 1 minute)
WAIT_TIME=0
while true; do
  if nc -w 1 -z "${LOCAL_REGISTRY_HOSTNAME}" 5000; then
    echo "Able to connect to ${LOCAL_REGISTRY_HOSTNAME} port 5000"
    break
  else
    if (( ${WAIT_TIME} >= 60 )); then
      echo "Timed out waiting for connection to ${LOCAL_REGISTRY_HOSTNAME} on port 5000. Exiting."
      exit 1
    fi
    echo "Waiting to connect to ${LOCAL_REGISTRY_HOSTNAME} on port 5000. Sleeping 5 seconds.."
    sleep 5
    WAIT_TIME=$((WAIT_TIME + 5))
  fi
done

# Wait another 5 seconds for good measure
sleep 5

# Run the example binary
bin/oras-acceptance-simple

# Ensure hello.txt exists and contains expected content
grep '^Hello World!$' hello.txt
