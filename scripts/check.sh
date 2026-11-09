#!/usr/bin/env bash
# The same checks as CI. Needs Go and Node (for the viewer type check).
set -euo pipefail
cd "$(dirname "$0")/.."

echo "== gofmt"
unformatted=$(gofmt -l .)
if [ -n "$unformatted" ]; then
  echo "not formatted:"; echo "$unformatted"; exit 1
fi

echo "== go vet"
go vet ./...

echo "== golangci-lint"
if command -v golangci-lint >/dev/null; then
  golangci-lint run ./...
else
  echo "skipped: install golangci-lint v2.15.0 to run it locally (CI always runs it)"
fi

echo "== tests (race)"
go test -race ./...

echo "== core stays pure and portable"
# The core never touches the clock, randomness, files or the network, so it
# stays deterministic and builds for WebAssembly.
bad=$(go list -f '{{.ImportPath}}: {{join .Imports " "}}' ./lattice ./script ./kernel | grep -E ' (time|math/rand|math/rand/v2|os|net|net/http|io/fs)( |$)' || true)
if [ -n "$bad" ]; then
  echo "forbidden import in the core:"; echo "$bad"; exit 1
fi
GOOS=js GOARCH=wasm go build ./lattice ./script ./kernel
GOOS=wasip1 GOARCH=wasm go build ./lattice ./script ./kernel

echo "== viewer type check"
npx --yes -p typescript@7.0.2 tsc -p web

echo "ok"
