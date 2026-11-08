//! `[d]` composes: bodies never decide anything, they travel toward the targets `[c]` and
//! the board give them. Springs make every state change feel physical. This layer is
//! cosmetic and client-only, so `f32` is fine (it is never part of lockstep).

/// Stiffness and damping shared by every runtime (the feel of the web prototypes).
pub const STIFFNESS: f32 = 190.0;
pub const DAMPING: f32 = 21.0;

/// One damped spring.
#[derive(Clone, Copy, Debug, Default, PartialEq)]
pub struct Spring {
    pub pos: f32,
    pub vel: f32,
    pub target: f32,
}

impl Spring {
    pub const fn at(v: f32) -> Self {
        Self {
            pos: v,
            vel: 0.0,
            target: v,
        }
    }

    /// Advances by `dt` seconds (semi-implicit Euler, sub-stepped for stability).
    /// Returns `true` while still moving.
    pub fn step(&mut self, dt: f32) -> bool {
        let n = (dt / (1.0 / 120.0)).ceil().clamp(1.0, 8.0);
        let h = dt / n;
        for _ in 0..n as u32 {
            let a = STIFFNESS * (self.target - self.pos) - DAMPING * self.vel;
            self.vel += a * h;
            self.pos += self.vel * h;
        }
        if (self.target - self.pos).abs() < 5e-4 && self.vel.abs() < 5e-3 {
            self.pos = self.target;
            self.vel = 0.0;
            return false;
        }
        true
    }

    pub fn settled(&self) -> bool {
        self.pos == self.target && self.vel == 0.0
    }
}

/// Many bodies as struct-of-arrays, written so LLVM vectorises the loop
/// (3.5-4x faster per core than the same loop in Go, see docs/rust-pivot.md).
#[derive(Clone, Debug, Default)]
pub struct Bodies {
    pub x: Vec<f32>,
    pub y: Vec<f32>,
    pub vx: Vec<f32>,
    pub vy: Vec<f32>,
    pub tx: Vec<f32>,
    pub ty: Vec<f32>,
}

impl Bodies {
    pub fn push(&mut self, x: f32, y: f32) {
        self.x.push(x);
        self.y.push(y);
        self.vx.push(0.0);
        self.vy.push(0.0);
        self.tx.push(x);
        self.ty.push(y);
    }

    pub fn len(&self) -> usize {
        self.x.len()
    }

    pub fn is_empty(&self) -> bool {
        self.x.is_empty()
    }

    pub fn step(&mut self, dt: f32) {
        let n = self.x.len();
        let (px, py, vx, vy) = (
            &mut self.x[..n],
            &mut self.y[..n],
            &mut self.vx[..n],
            &mut self.vy[..n],
        );
        let (tx, ty) = (&self.tx[..n], &self.ty[..n]);
        for i in 0..n {
            let ax = STIFFNESS * (tx[i] - px[i]) - DAMPING * vx[i];
            vx[i] += ax * dt;
            px[i] += vx[i] * dt;
            let ay = STIFFNESS * (ty[i] - py[i]) - DAMPING * vy[i];
            vy[i] += ay * dt;
            py[i] += vy[i] * dt;
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn spring_settles_on_target() {
        let mut s = Spring::at(0.0);
        s.target = 3.0;
        let mut frames = 0;
        while s.step(1.0 / 60.0) {
            frames += 1;
            assert!(frames < 120, "did not settle");
        }
        assert!(s.settled());
        assert!((s.pos - 3.0).abs() < 1e-6);
    }

    #[test]
    fn large_dt_stays_stable() {
        let mut s = Spring::at(0.0);
        s.target = 1.0;
        for _ in 0..20 {
            s.step(0.25);
            assert!(s.pos.abs() < 3.0, "exploded: {}", s.pos);
        }
    }

    #[test]
    fn bodies_converge() {
        let mut b = Bodies::default();
        b.push(0.0, 0.0);
        b.tx[0] = 10.0;
        for _ in 0..90 {
            b.step(1.0 / 60.0);
        }
        assert!((b.x[0] - 10.0).abs() < 0.05);
    }
}
