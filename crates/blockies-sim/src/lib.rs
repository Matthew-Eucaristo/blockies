//! The `[c]` block field: a deterministic falling-sand cellular automaton.
//!
//! Determinism is the contract that makes lockstep possible: the same spec, the same
//! intents in the same ticks give the same bytes on every CPU, thread count and in WASM.
//! So: integer math only, no clock, no randomness except [`mix`], no `HashMap` iteration.
//!
//! Rules (Margolus 2x2 blocks, alternating offset) are precomputed into a 256-entry table.
//! The grid is cut into 64x64 tiles; a tile is processed only if something within one tile
//! changed in the last two ticks, which is exact (see `sleeping_is_exact`). Tile rows are
//! disjoint `&mut [u8]` slices, so the `parallel` feature runs them on rayon with no locks.

#![deny(clippy::float_arithmetic)]

use blockies_model::{Material, Scene, SimSpec};
#[cfg(feature = "parallel")]
use rayon::prelude::*;

pub const CHUNK: usize = 64;
const EMPTY: u8 = Material::Empty as u8;
const SAND: u8 = Material::Sand as u8;
const WATER: u8 = Material::Water as u8;
const WALL: u8 = Material::Wall as u8;

/// Integer hash used for every pseudo-random choice.
#[inline]
pub const fn mix(x: u32, y: u32, t: u32, seed: u32) -> u32 {
    let mut h = seed
        ^ x.wrapping_mul(0x9E37_79B1)
        ^ y.wrapping_mul(0x85EB_CA77)
        ^ t.wrapping_mul(0xC2B2_AE3D);
    h ^= h >> 16;
    h = h.wrapping_mul(0x7FEB_352D);
    h ^= h >> 15;
    h = h.wrapping_mul(0x846C_A68B);
    h ^= h >> 16;
    h
}

const fn density(m: u8) -> u8 {
    match m {
        EMPTY => 0,
        WATER => 1,
        SAND => 2,
        _ => u8::MAX,
    }
}

const fn movable(m: u8) -> bool {
    m == SAND || m == WATER
}

const fn falls(top: u8, bottom: u8) -> bool {
    movable(top) && bottom != WALL && density(top) > density(bottom)
}

/// The block rule for one 2x2 block `a b / c d`. Returns the new `[a, b, c, d]`.
pub const fn rule(a: u8, b: u8, c: u8, d: u8) -> [u8; 4] {
    let (mut a, mut b, mut c, mut d) = (a, b, c, d);
    let mut moved = false;
    if falls(a, c) {
        (a, c) = (c, a);
        moved = true;
    }
    if falls(b, d) {
        (b, d) = (d, b);
        moved = true;
    }
    if moved {
        return [a, b, c, d];
    }
    if movable(a) && b == EMPTY && d == EMPTY {
        return [EMPTY, EMPTY, c, a];
    }
    if movable(b) && a == EMPTY && c == EMPTY {
        return [EMPTY, EMPTY, b, d];
    }
    if (c == WATER && d == EMPTY) || (d == WATER && c == EMPTY) {
        return [a, b, d, c];
    }
    if (a == WATER && b == EMPTY) || (b == WATER && a == EMPTY) {
        return [b, a, c, d];
    }
    [a, b, c, d]
}

const fn build_lut() -> [u8; 256] {
    let mut lut = [0u8; 256];
    let mut i = 0;
    while i < 256 {
        let k = i as u8;
        let r = rule(k & 3, (k >> 2) & 3, (k >> 4) & 3, k >> 6);
        lut[i] = r[0] | (r[1] << 2) | (r[2] << 4) | (r[3] << 6);
        i += 1;
    }
    lut
}

/// `rule` for all 256 packed blocks (`a | b<<2 | c<<4 | d<<6`), built at compile time.
pub static LUT: [u8; 256] = build_lut();

/// Display colours (RGBA) per material, used by every runtime.
pub const PALETTE: [[u8; 4]; 4] = [
    [0, 0, 0, 0],
    [214, 178, 96, 255],
    [64, 132, 230, 255],
    [120, 120, 130, 255],
];

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum SimError {
    Size { width: u32, height: u32 },
    Snapshot { expected: usize, got: usize },
}

impl std::fmt::Display for SimError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::Size { width, height } => {
                write!(
                    f,
                    "sim size {width}x{height}: both must be multiples of 64 between 128 and 8192"
                )
            }
            Self::Snapshot { expected, got } => {
                write!(f, "snapshot has {got} cells, expected {expected}")
            }
        }
    }
}

impl std::error::Error for SimError {}

#[derive(Debug, Clone)]
pub struct World {
    spec: SimSpec,
    w: usize,
    h: usize,
    tx: usize,
    ty: usize,
    cells: Vec<u8>,
    tick: u32,
    emitter_stride: Option<usize>,
    sleep: bool,
    parallel: bool,
    /// Ticks left during which every tile is processed (start, restore).
    wake_all: u8,
    changed: Vec<u8>,
    recent1: Vec<u8>,
    recent2: Vec<u8>,
    awake: Vec<u8>,
    awake_count: usize,
}

const fn emitter_stride(scene: Scene) -> Option<usize> {
    match scene {
        Scene::Empty => None,
        Scene::Calm => Some(16),
        Scene::Storm => Some(1),
    }
}

impl World {
    pub fn new(spec: SimSpec) -> Result<Self, SimError> {
        let (w, h) = (spec.width as usize, spec.height as usize);
        let ok = |v: usize| (2 * CHUNK..=8192).contains(&v) && v.is_multiple_of(CHUNK);
        if !ok(w) || !ok(h) {
            return Err(SimError::Size {
                width: spec.width,
                height: spec.height,
            });
        }
        let mut cells = vec![EMPTY; w * h];
        for (y, row) in cells.chunks_exact_mut(w).enumerate() {
            for (x, cell) in row.iter_mut().enumerate() {
                *cell = if y % 128 == 100 && (x / 32) % 3 != 0 {
                    WALL
                } else {
                    match spec.scene {
                        Scene::Empty => EMPTY,
                        Scene::Calm if y * 5 >= h * 3 => SAND,
                        Scene::Calm => EMPTY,
                        Scene::Storm if y * 5 < h * 3 => {
                            match mix(x as u32, y as u32, 0, spec.seed) % 100 {
                                0..25 => SAND,
                                25..37 => WATER,
                                _ => EMPTY,
                            }
                        }
                        Scene::Storm => EMPTY,
                    }
                };
            }
        }
        Ok(Self::with_cells(spec, 0, cells))
    }

    /// Rebuilds a world from a snapshot (`tick` + `cells`). Bit-identical to the original from
    /// then on: sleeping is exact, so starting with every tile awake changes nothing.
    pub fn restore(spec: SimSpec, tick: u32, cells: Vec<u8>) -> Result<Self, SimError> {
        let fresh = Self::new(spec)?;
        if cells.len() != fresh.cells.len() {
            return Err(SimError::Snapshot {
                expected: fresh.cells.len(),
                got: cells.len(),
            });
        }
        Ok(Self::with_cells(spec, tick, cells))
    }

    fn with_cells(spec: SimSpec, tick: u32, cells: Vec<u8>) -> Self {
        let (w, h) = (spec.width as usize, spec.height as usize);
        let tiles = (w / CHUNK) * (h / CHUNK);
        Self {
            spec,
            w,
            h,
            tx: w / CHUNK,
            ty: h / CHUNK,
            cells,
            tick,
            emitter_stride: emitter_stride(spec.scene),
            sleep: true,
            parallel: cfg!(feature = "parallel"),
            wake_all: 2,
            changed: vec![0; tiles],
            recent1: vec![0; tiles],
            recent2: vec![0; tiles],
            awake: vec![1; tiles],
            awake_count: tiles,
        }
    }

    pub const fn spec(&self) -> SimSpec {
        self.spec
    }

    pub const fn size(&self) -> (usize, usize) {
        (self.w, self.h)
    }

    pub const fn tick(&self) -> u32 {
        self.tick
    }

    pub fn cells(&self) -> &[u8] {
        &self.cells
    }

    pub fn get(&self, x: usize, y: usize) -> u8 {
        self.cells[y * self.w + x]
    }

    pub const fn tiles(&self) -> usize {
        self.tx * self.ty
    }

    /// Tiles processed on the last tick.
    pub const fn awake_count(&self) -> usize {
        self.awake_count
    }

    /// Turns chunk sleeping off (for tests and benchmarks; results are identical).
    pub fn set_sleep(&mut self, on: bool) {
        self.sleep = on;
    }

    /// Runs tile rows serially even when the `parallel` feature is on (results are identical).
    pub fn set_parallel(&mut self, on: bool) {
        self.parallel = on && cfg!(feature = "parallel");
    }

    /// FNV-1a 64 over all cells: the value clients compare in lockstep.
    pub fn hash(&self) -> u64 {
        self.cells.iter().fold(0xcbf2_9ce4_8422_2325, |h, &b| {
            (h ^ u64::from(b)).wrapping_mul(0x0000_0100_0000_01b3)
        })
    }

    /// Fills a disc with `m` (`Material::Empty` erases). Applied between ticks, in intent order.
    /// Coordinates outside the field are clipped.
    pub fn paint(&mut self, x: i32, y: i32, r: u16, m: Material) {
        let r = i32::from(r.min(512));
        let (w, h) = (self.w as i32, self.h as i32);
        for py in (y - r).max(0)..=(y + r).min(h - 1) {
            for px in (x - r).max(0)..=(x + r).min(w - 1) {
                let (dx, dy) = (px - x, py - y);
                if dx * dx + dy * dy > r * r {
                    continue;
                }
                let i = py as usize * self.w + px as usize;
                if self.cells[i] != m as u8 {
                    self.cells[i] = m as u8;
                    // Counts as a change on the previous tick, so neighbours wake up.
                    self.recent1[(py as usize / CHUNK) * self.tx + px as usize / CHUNK] = 1;
                }
            }
        }
    }

    pub fn step(&mut self) {
        let off = (self.tick & 1) as usize;
        self.compute_awake();
        self.changed.fill(0);
        self.emit();

        let Self {
            w,
            tx,
            cells,
            changed,
            awake,
            ..
        } = self;
        let (w, tx) = (*w, *tx);
        let awake: &[u8] = awake;
        let band = |(k, (band, flags)): (usize, (&mut [u8], &mut [u8]))| {
            process_band(band, flags, &awake[k * tx..(k + 1) * tx], w, off);
        };
        let bands = &mut cells[off * w..];
        #[cfg(feature = "parallel")]
        if self.parallel {
            bands
                .par_chunks_mut(CHUNK * w)
                .zip(changed.par_chunks_mut(tx))
                .enumerate()
                .for_each(band);
        } else {
            bands
                .chunks_mut(CHUNK * w)
                .zip(changed.chunks_mut(tx))
                .enumerate()
                .for_each(band);
        }
        #[cfg(not(feature = "parallel"))]
        bands
            .chunks_mut(CHUNK * w)
            .zip(changed.chunks_mut(tx))
            .enumerate()
            .for_each(band);

        std::mem::swap(&mut self.recent2, &mut self.recent1);
        std::mem::swap(&mut self.recent1, &mut self.changed);
        self.wake_all = self.wake_all.saturating_sub(1);
        self.tick = self.tick.wrapping_add(1);
    }

    fn compute_awake(&mut self) {
        let (tx, ty) = (self.tx, self.ty);
        if !self.sleep || self.wake_all > 0 {
            self.awake.fill(1);
            self.awake_count = tx * ty;
            return;
        }
        let emit_row = self.emitter_stride.is_some();
        let mut count = 0;
        for k in 0..ty {
            for c in 0..tx {
                let mut on = u8::from(k == 0 && emit_row);
                for kk in k.saturating_sub(1)..=(k + 1).min(ty - 1) {
                    for cc in c.saturating_sub(1)..=(c + 1).min(tx - 1) {
                        let i = kk * tx + cc;
                        on |= self.recent1[i] | self.recent2[i];
                    }
                }
                self.awake[k * tx + c] = on;
                count += usize::from(on);
            }
        }
        self.awake_count = count;
    }

    fn emit(&mut self) {
        let Some(stride) = self.emitter_stride else {
            return;
        };
        let t = self.tick.wrapping_add(1);
        for c in (0..self.tx).step_by(stride) {
            for x in c * CHUNK + 28..c * CHUNK + 36 {
                let r = mix(x as u32, 0, t, self.spec.seed);
                if r.is_multiple_of(4) && self.cells[x] == EMPTY {
                    self.cells[x] = if (r >> 8) & 1 == 0 { SAND } else { WATER };
                    self.changed[c] = 1;
                }
            }
        }
    }

    /// Writes the field as RGBA (4 bytes per cell) into `out`, resizing it.
    pub fn rgba(&self, out: &mut Vec<u8>) {
        out.clear();
        out.reserve(self.cells.len() * 4);
        for &c in &self.cells {
            out.extend_from_slice(&PALETTE[usize::from(c & 3)]);
        }
    }

    /// The most "solid" material in a rectangle, for downsampled views (wall > sand > water).
    pub fn sample(&self, x0: usize, y0: usize, x1: usize, y1: usize) -> u8 {
        const RANK: [u8; 4] = [0, 2, 1, 3];
        let mut best = EMPTY;
        let (x1, y1) = (x1.min(self.w), y1.min(self.h));
        // Stride keeps very zoomed-out views cheap: at most ~16 probes per axis.
        let sx = ((x1.saturating_sub(x0)) / 16).max(1);
        let sy = ((y1.saturating_sub(y0)) / 16).max(1);
        for y in (y0..y1).step_by(sy) {
            let row = &self.cells[y * self.w..(y + 1) * self.w];
            for &c in row[x0.min(x1)..x1].iter().step_by(sx) {
                if RANK[usize::from(c)] > RANK[usize::from(best)] {
                    best = c;
                    if best == WALL {
                        return best;
                    }
                }
            }
        }
        best
    }
}

/// Processes one tile row: up to 64 grid rows starting at `64k + off`.
#[inline]
fn process_band(band: &mut [u8], flags: &mut [u8], awake: &[u8], w: usize, off: usize) {
    let rows = band.len() / w;
    let mut r0 = 0;
    while r0 + 1 < rows {
        let (top, bot) = band[r0 * w..(r0 + 2) * w].split_at_mut(w);
        for (c, (&on, flag)) in awake.iter().zip(flags.iter_mut()).enumerate() {
            if on == 0 {
                continue;
            }
            let xs = c * CHUNK + off;
            let xe = xs + 2 * (CHUNK / 2).min((w - xs) / 2);
            let mut changed = 0u8;
            let (tp, bp) = (
                top[xs..xe].as_chunks_mut::<2>().0,
                bot[xs..xe].as_chunks_mut::<2>().0,
            );
            for (t, b) in tp.iter_mut().zip(bp) {
                let idx = t[0] | (t[1] << 2) | (b[0] << 4) | (b[1] << 6);
                let out = LUT[idx as usize];
                if out != idx {
                    t[0] = out & 3;
                    t[1] = (out >> 2) & 3;
                    b[0] = (out >> 4) & 3;
                    b[1] = out >> 6;
                    changed = 1;
                }
            }
            *flag |= changed;
        }
        r0 += 2;
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    /// Golden values from the Go vs Rust benchmark spec (both agreed on them).
    const GOLDEN_STORM_256: u64 = 0x6332_fb16_da4b_95fa;
    const GOLDEN_CALM_256: u64 = 0xcb6e_9b3b_8be9_36cf;

    fn spec(size: u32, scene: Scene) -> SimSpec {
        SimSpec {
            width: size,
            height: size,
            scene,
            seed: 7,
        }
    }

    fn run(scene: Scene, size: u32, ticks: u32, sleep: bool, parallel: bool) -> World {
        let mut w = World::new(spec(size, scene)).unwrap();
        w.set_sleep(sleep);
        w.set_parallel(parallel);
        for _ in 0..ticks {
            w.step();
        }
        w
    }

    #[test]
    fn lut_conserves_materials_and_walls() {
        for i in 0..=255u8 {
            let out = LUT[i as usize];
            for m in 0..4 {
                let n = |k: u8| (0..4).filter(|s| (k >> (2 * s)) & 3 == m).count();
                assert_eq!(n(i), n(out), "block {i:#010b}");
            }
            for s in 0..4 {
                if (i >> (2 * s)) & 3 == WALL {
                    assert_eq!((out >> (2 * s)) & 3, WALL);
                }
            }
        }
    }

    #[test]
    fn golden_hashes_match_the_benchmark_spec() {
        assert_eq!(
            run(Scene::Storm, 256, 500, true, true).hash(),
            GOLDEN_STORM_256
        );
        assert_eq!(
            run(Scene::Calm, 256, 500, true, true).hash(),
            GOLDEN_CALM_256
        );
    }

    #[test]
    fn sleeping_is_exact() {
        for scene in Scene::ALL {
            for size in [128, 320, 1088] {
                let on = run(scene, size, 300, true, true);
                let off = run(scene, size, 300, false, true);
                assert_eq!(on.hash(), off.hash(), "{scene:?} {size}");
            }
        }
    }

    #[test]
    fn threads_do_not_change_result() {
        assert_eq!(
            run(Scene::Storm, 512, 200, true, true).hash(),
            run(Scene::Storm, 512, 200, true, false).hash()
        );
    }

    #[test]
    fn paint_is_deterministic_and_wakes_sleeping_tiles() {
        let play = |sleep: bool| {
            let mut w = World::new(spec(512, Scene::Calm)).unwrap();
            w.set_sleep(sleep);
            for t in 0..300 {
                if t % 50 == 10 {
                    w.paint(100 + t, 40, 12, Material::Sand);
                    w.paint(300, 200, 20, Material::Empty);
                }
                w.step();
            }
            w.hash()
        };
        assert_eq!(play(true), play(false));
    }

    #[test]
    fn restore_continues_identically() {
        let mut a = run(Scene::Storm, 256, 137, true, true);
        let mut b = World::restore(a.spec(), a.tick(), a.cells().to_vec()).unwrap();
        for _ in 0..200 {
            a.step();
            b.step();
        }
        assert_eq!(a.hash(), b.hash());
        assert!(World::restore(a.spec(), 0, vec![0; 10]).is_err());
    }

    #[test]
    fn calm_world_mostly_sleeps() {
        let w = run(Scene::Calm, 2048, 50, true, true);
        assert!(
            w.awake_count() * 4 < w.tiles(),
            "{} of {}",
            w.awake_count(),
            w.tiles()
        );
    }

    #[test]
    fn bad_sizes_are_rejected() {
        assert!(World::new(spec(100, Scene::Empty)).is_err());
        assert!(World::new(spec(64, Scene::Empty)).is_err());
    }
}
