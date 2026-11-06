//! The browser and Node get the exact engine the server and TUI run: same crate, same
//! determinism, compiled to `wasm32-unknown-unknown` (single-threaded, no rayon).
//!
//! `Field::pixels()` returns a pointer into WASM memory, so a WebGL texture can be uploaded
//! straight from it without copying (the Sandspiel pattern).

use blockies_model::{Material, Scene, SimSpec};
use blockies_protocol::{ServerMsg, Snapshot, hash_hex};
use blockies_sim::World;
use wasm_bindgen::prelude::*;

/// A block field running in lockstep with a server (or alone).
#[wasm_bindgen]
#[derive(Debug)]
pub struct Field {
    world: World,
    rgba: Vec<u8>,
}

#[wasm_bindgen]
impl Field {
    #[wasm_bindgen(constructor)]
    pub fn new(width: u32, height: u32, scene: &str, seed: u32) -> Result<Field, JsError> {
        let scene = Scene::parse(scene)
            .ok_or_else(|| JsError::new("scene must be empty, calm or storm"))?;
        let world = World::new(SimSpec {
            width,
            height,
            scene,
            seed,
        })
        .map_err(|e| JsError::new(&e.to_string()))?;
        Ok(Self {
            world,
            rgba: Vec::new(),
        })
    }

    /// Restores a field from a server `Snapshot` (JSON of `{spec, tick, cells}`).
    #[wasm_bindgen(js_name = fromSnapshot)]
    pub fn from_snapshot(json: &str) -> Result<Field, JsError> {
        let snap: Snapshot =
            serde_json::from_str(json).map_err(|e| JsError::new(&e.to_string()))?;
        let world = snap.world().map_err(|e| JsError::new(&e.to_string()))?;
        Ok(Self {
            world,
            rgba: Vec::new(),
        })
    }

    pub fn step(&mut self, ticks: u32) {
        for _ in 0..ticks {
            self.world.step();
        }
    }

    /// Applies one server `Tick` message (JSON). Returns false if it is not the next tick,
    /// in which case the caller should send `{"t":"resync"}`.
    #[wasm_bindgen(js_name = applyTick)]
    pub fn apply_tick(&mut self, json: &str) -> Result<bool, JsError> {
        match serde_json::from_str(json).map_err(|e| JsError::new(&e.to_string()))? {
            ServerMsg::Tick { tick, paints } if tick == self.world.tick() => {
                for p in &paints {
                    self.world.paint(p.x, p.y, p.r, p.material);
                }
                self.world.step();
                Ok(true)
            }
            ServerMsg::Tick { .. } => Ok(false),
            _ => Err(JsError::new("not a tick message")),
        }
    }

    pub fn paint(&mut self, x: i32, y: i32, r: u16, material: &str) -> Result<(), JsError> {
        let m = Material::parse(material)
            .ok_or_else(|| JsError::new("material must be sand, water, wall or empty"))?;
        self.world.paint(x, y, r, m);
        Ok(())
    }

    pub fn tick(&self) -> u32 {
        self.world.tick()
    }

    pub fn width(&self) -> u32 {
        self.world.spec().width
    }

    pub fn height(&self) -> u32 {
        self.world.spec().height
    }

    /// Lockstep hash (hex).
    pub fn hash(&self) -> String {
        hash_hex(self.world.hash())
    }

    /// Renders RGBA (4 bytes per cell) and returns a pointer into WASM memory.
    pub fn pixels(&mut self) -> *const u8 {
        self.world.rgba(&mut self.rgba);
        self.rgba.as_ptr()
    }
}

/// Checks DSL: `{"pieces": n, "errors": [{"line", "message"}]}`.
#[wasm_bindgen]
pub fn check(dsl: &str) -> String {
    let p = blockies_dsl::parse(dsl);
    serde_json::json!({ "pieces": p.board.pieces.len(), "errors": p.errors }).to_string()
}

/// Canonical DSL.
#[wasm_bindgen(js_name = formatDsl)]
pub fn format_dsl(dsl: &str) -> String {
    blockies_dsl::format(&blockies_dsl::parse(dsl).board)
}

#[wasm_bindgen(js_name = agentPrompt)]
pub fn agent_prompt() -> String {
    blockies_dsl::AGENT_PROMPT.to_string()
}

/// Numbers-only entry point used by `scripts/wasm-determinism.mjs` to compare WASM with
/// native without JS glue: hash after `ticks` ticks of a square field.
#[wasm_bindgen(js_name = hashAfter)]
pub fn hash_after(size: u32, scene: u32, seed: u32, ticks: u32) -> u64 {
    let scene = Scene::ALL[(scene as usize).min(2)];
    let mut w = World::new(SimSpec {
        width: size,
        height: size,
        scene,
        seed,
    })
    .expect("valid size");
    for t in 0..ticks {
        if t % 50 == 10 {
            w.paint(40 + t as i32, 20, 6, Material::Water);
        }
        w.step();
    }
    w.hash()
}

#[cfg(test)]
mod tests {
    use super::*;

    /// The values `scripts/wasm-determinism.mjs` must reproduce inside WebAssembly.
    pub const NATIVE: [(u32, u32, u32, u32); 3] =
        [(256, 2, 7, 500), (512, 1, 3, 300), (128, 0, 1, 200)];

    #[test]
    fn print_native_hashes() {
        for (size, scene, seed, ticks) in NATIVE {
            println!(
                "{size} {scene} {seed} {ticks} {:016x}",
                hash_after(size, scene, seed, ticks)
            );
        }
    }
}
