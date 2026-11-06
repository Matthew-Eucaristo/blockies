#!/bin/sh
# Runs the spike in a pseudo-terminal at several sizes and prints the measurements.
# Usage: ./bench.sh [seconds]
set -e
cd "$(dirname "$0")"
go build -o bin/tui .
secs=${1:-18}
for cfg in "120 40 60" "200 60 60" "200 60 30"; do
  set -- $cfg
  out="stats-$1x$2-$3fps.json"
  script -q /dev/null sh -c "stty cols $1 rows $2; ./bin/tui -demo -duration ${secs}s -fps $3 -stats $out" </dev/null >/dev/null 2>&1 || true
  echo "== $1x$2 @ $3fps"
  cat "$out"; echo
done
