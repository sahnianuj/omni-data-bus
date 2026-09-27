#!/bin/bash
# Benchmark harness host: Go (to run odbbench) and the psql client (to generate datasets).
set -euxo pipefail
apt-get update -y && apt-get install -y postgresql-client curl git
curl -fsSL https://go.dev/dl/go1.26.0.linux-amd64.tar.gz | tar -C /usr/local -xz
echo 'export PATH=$PATH:/usr/local/go/bin' > /etc/profile.d/go.sh
