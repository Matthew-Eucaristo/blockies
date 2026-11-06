//! `cargo run --release -p blockies-sim --example bench`: ms per tick for a few field sizes.
use blockies_model::{Scene, SimSpec};
use blockies_sim::World;
use std::time::Instant;

fn main() {
    println!(
        "{:<6} {:>6} {:>8} {:>8} {:>10} {:>7}",
        "scene", "cells", "serial", "parallel", "Mcells/s", "awake"
    );
    for scene in [Scene::Storm, Scene::Calm] {
        for size in [1024u32, 2048, 4096] {
            let spec = SimSpec {
                width: size,
                height: size,
                scene,
                seed: 7,
            };
            let mut row = Vec::new();
            let mut awake = 0;
            for parallel in [false, true] {
                let mut w = World::new(spec).unwrap();
                w.set_parallel(parallel);
                let ticks = 200;
                let t = Instant::now();
                awake = 0;
                for _ in 0..ticks {
                    w.step();
                    awake += w.awake_count();
                }
                row.push(t.elapsed().as_secs_f64() * 1e3 / f64::from(ticks));
                awake = 100 * awake / (ticks as usize * w.tiles());
            }
            let cells = f64::from(size) * f64::from(size);
            println!(
                "{:<6} {:>5.0}M {:>6.2}ms {:>6.2}ms {:>10.0} {:>6}%",
                scene.name(),
                cells / 1e6,
                row[0],
                row[1],
                cells / (row[1] / 1e3) / 1e6,
                awake
            );
        }
    }
}
