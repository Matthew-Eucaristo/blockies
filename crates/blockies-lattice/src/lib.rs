//! A lattice that never knows its own dimension.
//!
//! Proposal from `explorations/` (2026-11-09), not used by the core yet. A world is a
//! [`Shape`] of rank 1 to 4, and a cell is a [`Coord`]. Nothing here names x, y or z:
//! neighbours are "one step along any axis", so the same rule, placement or body growth
//! works on a line, a board, a voxel world, or a board stacked over time.
//!
//! Rank 4 is the ceiling on purpose: voxels (3) plus time (1). Renderers decide what an
//! axis means on screen (a board, voxels, a sequence diagram, a sculpture). The core only
//! hands them cells.
//!
//! Integer-only, no allocation for coordinates, no hash-map iteration: everything is
//! deterministic and compiles for `wasm32-unknown-unknown`.

use std::collections::{BTreeSet, VecDeque};

/// Highest supported rank: voxels plus time.
pub const MAX_RANK: usize = 4;

/// A cell position. Axes at or above the shape's rank are always 0.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, PartialOrd, Ord, Hash)]
pub struct Coord(pub [i32; MAX_RANK]);

impl Coord {
    /// Builds a coordinate from up to [`MAX_RANK`] components (the rest are 0).
    pub fn new(c: &[i32]) -> Self {
        let mut a = [0; MAX_RANK];
        for (slot, v) in a.iter_mut().zip(c) {
            *slot = *v;
        }
        Self(a)
    }

    pub fn offset(self, d: Self) -> Self {
        let mut a = self.0;
        for (v, dv) in a.iter_mut().zip(d.0) {
            *v += dv;
        }
        Self(a)
    }

    pub fn manhattan(self, other: Self) -> u32 {
        self.0.iter().zip(other.0).map(|(a, b)| a.abs_diff(b)).sum()
    }
}

/// Why a shape was refused.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum ShapeError {
    /// Rank must be 1..=4.
    Rank(usize),
    /// Every extent must be at least 1.
    Empty,
    /// The cell count does not fit in a `u32`.
    TooBig,
}

/// The extent of a world along each of its axes.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct Shape {
    dims: [u32; MAX_RANK],
    rank: u8,
    size: u32,
}

impl Shape {
    pub fn new(dims: &[u32]) -> Result<Self, ShapeError> {
        if dims.is_empty() || dims.len() > MAX_RANK {
            return Err(ShapeError::Rank(dims.len()));
        }
        if dims.contains(&0) {
            return Err(ShapeError::Empty);
        }
        let mut d = [1; MAX_RANK];
        let mut size: u32 = 1;
        for (slot, v) in d.iter_mut().zip(dims) {
            *slot = *v;
            size = size.checked_mul(*v).ok_or(ShapeError::TooBig)?;
        }
        Ok(Self {
            dims: d,
            rank: dims.len() as u8,
            size,
        })
    }

    pub fn rank(&self) -> usize {
        usize::from(self.rank)
    }

    pub fn dims(&self) -> &[u32] {
        &self.dims[..self.rank()]
    }

    /// Number of cells.
    pub fn size(&self) -> u32 {
        self.size
    }

    pub fn contains(&self, c: Coord) -> bool {
        c.0.iter()
            .zip(self.dims)
            .enumerate()
            .all(|(axis, (&v, d))| {
                if axis < self.rank() {
                    v >= 0 && (v as u32) < d
                } else {
                    v == 0
                }
            })
    }

    /// Row-major index (axis 0 varies fastest), or `None` outside the shape.
    pub fn index(&self, c: Coord) -> Option<u32> {
        if !self.contains(c) {
            return None;
        }
        let mut i = 0;
        let mut stride = 1;
        for axis in 0..self.rank() {
            i += c.0[axis] as u32 * stride;
            stride *= self.dims[axis];
        }
        Some(i)
    }

    pub fn coord(&self, mut i: u32) -> Coord {
        let mut a = [0; MAX_RANK];
        for (axis, slot) in a.iter_mut().enumerate().take(self.rank()) {
            *slot = (i % self.dims[axis]) as i32;
            i /= self.dims[axis];
        }
        Coord(a)
    }

    /// The `2 * rank` face directions, in a fixed order: +axis0, -axis0, +axis1, ...
    pub fn dirs(&self) -> impl Iterator<Item = Coord> + '_ {
        (0..self.rank()).flat_map(|axis| {
            [1, -1].into_iter().map(move |sign| {
                let mut a = [0; MAX_RANK];
                a[axis] = sign;
                Coord(a)
            })
        })
    }

    /// Face neighbours inside the shape, in [`Shape::dirs`] order.
    pub fn neighbors(&self, c: Coord) -> impl Iterator<Item = Coord> + '_ {
        self.dirs()
            .map(move |d| c.offset(d))
            .filter(|n| self.contains(*n))
    }

    /// The same world with time as one more axis (how space-time views are built).
    pub fn with_time(&self, ticks: u32) -> Result<Self, ShapeError> {
        let mut d = self.dims().to_vec();
        d.push(ticks);
        Self::new(&d)
    }

    /// Nearest cell to `from` (breadth-first over faces) that is not in `taken` and passes
    /// `ok`. Ties break by [`Shape::dirs`] order, so placement is deterministic. This is
    /// how children and bodies find room without the agent naming a coordinate.
    pub fn nearest_free(
        &self,
        from: &[Coord],
        taken: &BTreeSet<Coord>,
        mut ok: impl FnMut(Coord) -> bool,
    ) -> Option<Coord> {
        let mut seen: BTreeSet<Coord> = from.iter().copied().collect();
        let mut queue: VecDeque<Coord> = from.iter().copied().collect();
        while let Some(c) = queue.pop_front() {
            for n in self.neighbors(c) {
                if !seen.insert(n) {
                    continue;
                }
                if !taken.contains(&n) && ok(n) {
                    return Some(n);
                }
                queue.push_back(n);
            }
        }
        None
    }
}

/// Axis-by-axis path from `a` to `b`, both ends included. Works at any rank.
pub fn path(a: Coord, b: Coord) -> Vec<Coord> {
    let mut out = vec![a];
    let mut c = a;
    for axis in 0..MAX_RANK {
        while c.0[axis] != b.0[axis] {
            c.0[axis] += (b.0[axis] - c.0[axis]).signum();
            out.push(c);
        }
    }
    out
}

#[cfg(test)]
mod tests {
    use super::*;

    fn c(v: &[i32]) -> Coord {
        Coord::new(v)
    }

    #[test]
    fn index_and_coord_round_trip_at_every_rank() {
        for dims in [&[7][..], &[5, 3], &[4, 3, 2], &[3, 2, 2, 5]] {
            let s = Shape::new(dims).unwrap();
            for i in 0..s.size() {
                assert_eq!(s.index(s.coord(i)), Some(i), "{dims:?} {i}");
            }
        }
    }

    #[test]
    fn neighbours_are_two_per_axis_inside() {
        let line = Shape::new(&[5]).unwrap();
        assert_eq!(line.neighbors(c(&[2])).count(), 2);
        assert_eq!(line.neighbors(c(&[0])).count(), 1);
        let board = Shape::new(&[5, 5]).unwrap();
        assert_eq!(board.neighbors(c(&[2, 2])).count(), 4);
        assert_eq!(board.neighbors(c(&[0, 0])).count(), 2);
        let voxels = Shape::new(&[5, 5, 5]).unwrap();
        assert_eq!(voxels.neighbors(c(&[2, 2, 2])).count(), 6);
        assert_eq!(voxels.neighbors(c(&[0, 0, 0])).count(), 3);
    }

    #[test]
    fn unused_axes_must_be_zero() {
        let board = Shape::new(&[4, 4]).unwrap();
        assert!(board.contains(c(&[1, 1])));
        assert!(!board.contains(c(&[1, 1, 1])));
        assert_eq!(board.index(c(&[1, 1, 1])), None);
    }

    #[test]
    fn bad_shapes_are_refused() {
        assert_eq!(Shape::new(&[]), Err(ShapeError::Rank(0)));
        assert_eq!(Shape::new(&[1, 1, 1, 1, 1]), Err(ShapeError::Rank(5)));
        assert_eq!(Shape::new(&[3, 0]), Err(ShapeError::Empty));
        assert_eq!(Shape::new(&[65536, 65536]), Err(ShapeError::TooBig));
    }

    #[test]
    fn time_is_one_more_axis() {
        let board = Shape::new(&[8, 6]).unwrap();
        let history = board.with_time(30).unwrap();
        assert_eq!(history.dims(), &[8, 6, 30]);
        let voxels_over_time = Shape::new(&[4, 4, 4]).unwrap().with_time(10).unwrap();
        assert_eq!(voxels_over_time.rank(), 4);
        assert!(voxels_over_time.with_time(2).is_err());
    }

    #[test]
    fn path_length_is_manhattan_distance() {
        let (a, b) = (c(&[1, 5, 2]), c(&[4, 2, 0]));
        let p = path(a, b);
        assert_eq!(p.len() as u32, a.manhattan(b) + 1);
        assert_eq!((p[0], *p.last().unwrap()), (a, b));
        assert!(p.windows(2).all(|w| w[0].manhattan(w[1]) == 1));
    }

    #[test]
    fn nearest_free_is_deterministic_and_respects_rules() {
        let s = Shape::new(&[5, 5]).unwrap();
        let center = c(&[2, 2]);
        let mut taken = BTreeSet::from([center]);
        // First free neighbour in dirs order: +axis0.
        assert_eq!(
            s.nearest_free(&[center], &taken, |_| true),
            Some(c(&[3, 2]))
        );
        taken.insert(c(&[3, 2]));
        assert_eq!(
            s.nearest_free(&[center], &taken, |_| true),
            Some(c(&[1, 2]))
        );
        // A rule (here: stay on the top row) pushes the search outward.
        assert_eq!(
            s.nearest_free(&[center], &taken, |n| n.0[1] == 0),
            Some(c(&[2, 0]))
        );
        // The same call in 3D finds a cell on the new axis once the plane is full.
        let v = Shape::new(&[1, 1, 3]).unwrap();
        assert_eq!(
            v.nearest_free(&[c(&[0, 0, 0])], &BTreeSet::new(), |_| true),
            Some(c(&[0, 0, 1]))
        );
    }
}
