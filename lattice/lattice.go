// Package lattice is a grid that never knows its own dimension.
//
// A world is a [Shape] of rank 1 to 4 and a cell is a [Coord]. Nothing here
// names x, y or z: a neighbour is one step along any axis, so the same
// placement or growth rule works on a line, a board, a voxel world, or a board
// stacked over time. Rank 4 is the ceiling on purpose: voxels plus time.
// Renderers decide what an axis means on screen.
//
// Everything is integer-only and deterministic: ties always break in
// [Shape.Dirs] order.
package lattice

import (
	"errors"
	"fmt"
)

// MaxRank is the highest supported rank: voxels plus time.
const MaxRank = 4

// MaxCells bounds the size of one world.
const MaxCells = 1 << 20

// Coord is a cell position. Axes at or above the shape's rank are always 0.
type Coord [MaxRank]int

// C builds a coordinate from up to [MaxRank] components (the rest are 0).
func C(v ...int) Coord {
	var c Coord
	copy(c[:], v)
	return c
}

// Add returns c moved by d.
func (c Coord) Add(d Coord) Coord {
	for i := range c {
		c[i] += d[i]
	}
	return c
}

// Manhattan is the number of face steps between c and d.
func (c Coord) Manhattan(d Coord) int {
	n := 0
	for i := range c {
		n += abs(c[i] - d[i])
	}
	return n
}

// Errors returned by [New].
var (
	ErrRank   = errors.New("rank must be 1 to 4")
	ErrEmpty  = errors.New("every size must be at least 1")
	ErrTooBig = fmt.Errorf("a world holds at most %d cells", MaxCells)
)

// Shape is the extent of a world along each of its axes.
type Shape struct {
	dims [MaxRank]int
	rank int
	size int
}

// New makes a shape with one size per axis.
func New(dims ...int) (Shape, error) {
	if len(dims) == 0 || len(dims) > MaxRank {
		return Shape{}, ErrRank
	}
	s := Shape{rank: len(dims), size: 1}
	for i := range s.dims {
		s.dims[i] = 1
	}
	for i, d := range dims {
		if d < 1 {
			return Shape{}, ErrEmpty
		}
		if s.size > MaxCells/d {
			return Shape{}, ErrTooBig
		}
		s.dims[i] = d
		s.size *= d
	}
	return s, nil
}

// Rank is the number of axes.
func (s Shape) Rank() int { return s.rank }

// Dims returns one size per axis.
func (s Shape) Dims() []int { return append([]int(nil), s.dims[:s.rank]...) }

// Size is the number of cells.
func (s Shape) Size() int { return s.size }

// Contains reports whether c is inside the shape.
func (s Shape) Contains(c Coord) bool {
	for i, v := range c {
		if i < s.rank {
			if v < 0 || v >= s.dims[i] {
				return false
			}
		} else if v != 0 {
			return false
		}
	}
	return true
}

// Index is the row-major index of c (axis 0 varies fastest).
func (s Shape) Index(c Coord) (int, bool) {
	if !s.Contains(c) {
		return 0, false
	}
	i, stride := 0, 1
	for a := 0; a < s.rank; a++ {
		i += c[a] * stride
		stride *= s.dims[a]
	}
	return i, true
}

// Coord is the inverse of [Shape.Index].
func (s Shape) Coord(i int) Coord {
	var c Coord
	for a := 0; a < s.rank; a++ {
		c[a] = i % s.dims[a]
		i /= s.dims[a]
	}
	return c
}

// Dirs returns the 2×rank face directions in a fixed order: +axis0, -axis0, +axis1, ...
func (s Shape) Dirs() []Coord {
	out := make([]Coord, 0, 2*s.rank)
	for a := 0; a < s.rank; a++ {
		for _, sign := range [2]int{1, -1} {
			var d Coord
			d[a] = sign
			out = append(out, d)
		}
	}
	return out
}

// Neighbors returns the face neighbours of c inside the shape, in [Shape.Dirs] order.
func (s Shape) Neighbors(c Coord) []Coord {
	out := make([]Coord, 0, 2*s.rank)
	for _, d := range s.Dirs() {
		if n := c.Add(d); s.Contains(n) {
			out = append(out, n)
		}
	}
	return out
}

// Around returns every other cell within one step along each axis at once
// (the 3^rank - 1 cells of the Moore neighbourhood), inside the shape, in a
// fixed order.
func (s Shape) Around(c Coord) []Coord {
	var out []Coord
	var walk func(axis int, d Coord)
	walk = func(axis int, d Coord) {
		if axis == s.rank {
			if d != (Coord{}) {
				if n := c.Add(d); s.Contains(n) {
					out = append(out, n)
				}
			}
			return
		}
		for _, v := range [3]int{-1, 0, 1} {
			d[axis] = v
			walk(axis+1, d)
		}
	}
	walk(0, Coord{})
	return out
}

// WithTime is the same world with time as one more axis (how space-time views are built).
func (s Shape) WithTime(ticks int) (Shape, error) {
	return New(append(s.Dims(), ticks)...)
}

// Center is the middle cell of the shape.
func (s Shape) Center() Coord {
	var c Coord
	for a := 0; a < s.rank; a++ {
		c[a] = (s.dims[a] - 1) / 2
	}
	return c
}

// NearestFree searches breadth-first over faces from the given cells and
// returns the first cell that is not taken and passes ok. The start cells
// themselves are never returned. Ties break in [Shape.Dirs] order, so the
// result is deterministic. This is how children and bodies find room without
// anyone naming a coordinate.
func (s Shape) NearestFree(from []Coord, taken, ok func(Coord) bool) (Coord, bool) {
	seen := make(map[Coord]bool, len(from)*8)
	queue := make([]Coord, 0, len(from)*4)
	for _, c := range from {
		if !seen[c] {
			seen[c] = true
			queue = append(queue, c)
		}
	}
	for head := 0; head < len(queue); head++ {
		for _, n := range s.Neighbors(queue[head]) {
			if seen[n] {
				continue
			}
			seen[n] = true
			if !taken(n) && ok(n) {
				return n, true
			}
			queue = append(queue, n)
		}
	}
	return Coord{}, false
}

// Path steps axis by axis from a to b, both ends included. Works at any rank.
func Path(a, b Coord) []Coord {
	out := []Coord{a}
	c := a
	for axis := range c {
		for c[axis] != b[axis] {
			if b[axis] > c[axis] {
				c[axis]++
			} else {
				c[axis]--
			}
			out = append(out, c)
		}
	}
	return out
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
