// Runs the sim inside WebAssembly (Node) and compares hashes with the native build.
// Usage: node scripts/wasm-determinism.mjs <path/to/blockies_wasm.wasm> < native-hashes.txt
// Each stdin line: "<size> <scene> <seed> <ticks> <hex hash>".
import { readFileSync } from 'node:fs';

const bytes = readFileSync(process.argv[2]);
const mod = await WebAssembly.compile(bytes);
// hashAfter only takes and returns numbers, so wasm-bindgen's JS glue is not needed:
// every import is stubbed and must never be called.
const imports = {};
for (const imp of WebAssembly.Module.imports(mod)) {
  (imports[imp.module] ??= {})[imp.name] = () => { throw new Error(`unexpected import ${imp.module}.${imp.name}`); };
}
const { exports } = await WebAssembly.instantiate(mod, imports);
// Before wasm-bindgen's CLI post-processes it, exports carry a hash suffix: hashAfter_<hash>.
const hashAfter = Object.entries(exports).find(([name]) => name === 'hashAfter' || name.startsWith('hashAfter_'))?.[1];
if (typeof hashAfter !== 'function') throw new Error('hashAfter export not found');
let failed = 0;
for (const line of readFileSync(0, 'utf8').trim().split('\n')) {
  const [size, scene, seed, ticks, want] = line.trim().split(/\s+/);
  const got = BigInt.asUintN(64, BigInt(hashAfter(+size, +scene, +seed, +ticks))).toString(16).padStart(16, '0');
  const ok = got === want;
  failed += ok ? 0 : 1;
  console.log(`${ok ? 'ok  ' : 'FAIL'} size=${size} scene=${scene} seed=${seed} ticks=${ticks} wasm=${got} native=${want}`);
}
process.exit(failed ? 1 : 0);
