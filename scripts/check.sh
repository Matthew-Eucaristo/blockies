#!/usr/bin/env bash
# Everything CI runs, locally. Usage: scripts/check.sh
set -euo pipefail
cd "$(dirname "$0")/.."

cargo fmt --all --check
cargo clippy --workspace --all-targets -- -D warnings
cargo clippy -p blockies-wasm -p blockies-sim -p blockies-lattice --no-default-features --target wasm32-unknown-unknown -- -D warnings
cargo test --workspace

# Determinism across targets: the WASM build must reproduce the native hashes.
cargo build -p blockies-wasm --target wasm32-unknown-unknown --profile release-wasm
cargo test -p blockies-wasm --quiet -- --nocapture print_native_hashes | grep -E '^[0-9]+ [0-9]' > target/native-hashes.txt
node scripts/wasm-determinism.mjs target/wasm32-unknown-unknown/release-wasm/blockies_wasm.wasm < target/native-hashes.txt
