#!/bin/bash
set -euo pipefail

echo "Building remotecmd-cli..."

go build -ldflags "-s -w" -o remotecmd-cli .

echo "Done: ./remotecmd-cli"
ls -lh remotecmd-cli
