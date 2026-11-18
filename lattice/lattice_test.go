package lattice

import (
	"errors"
	"testing"
)

func TestIndexRoundTripAtEveryRank(t *testing.T) {
	for _, dims := range [][]int{{7}, {5, 3}, {4, 3, 2}, {3, 2, 2, 5}} {
		s, err := New(dims...)
		if err != nil {
			t.Fatal(err)
		}
		for i := range s.Size() {
			if got, ok := s.Index(s.Coord(i)); !ok || got != i {
				t.Fatalf("%v: index(coord(%d)) = %d, %v", dims, i, got, ok)
			}
		}
	}
}

func TestNeighboursAreTwoPerAxis(t *testing.T) {
	cases := []struct {
		dims []int
		at   Coord
		want int
	}{
		{[]int{5}, C(2), 2},
		{[]int{5}, C(0), 1},
		{[]int{5, 5}, C(2, 2), 4},
		{[]int{5, 5}, C(0, 0), 2},
		{[]int{5, 5, 5}, C(2, 2, 2), 6},
		{[]int{5, 5, 5}, C(0, 0, 0), 3},
	}
	for _, c := range cases {
		s, _ := New(c.dims...)
		if got := len(s.Neighbors(c.at)); got != c.want {
			t.Errorf("%v at %v: %d neighbours, want %d", c.dims, c.at, got, c.want)
		}
	}
}

func TestUnusedAxesMustBeZero(t *testing.T) {
	s, _ := New(4, 4)
	if !s.Contains(C(1, 1)) || s.Contains(C(1, 1, 1)) {
		t.Fatal("unused axis accepted")
	}
	if _, ok := s.Index(C(1, 1, 1)); ok {
		t.Fatal("index outside the shape")
	}
}

func TestBadShapesAreRefused(t *testing.T) {
	for _, c := range []struct {
		dims []int
		err  error
	}{
		{nil, ErrRank},
		{[]int{1, 1, 1, 1, 1}, ErrRank},
		{[]int{3, 0}, ErrEmpty},
		{[]int{2048, 2048}, ErrTooBig},
	} {
		if _, err := New(c.dims...); !errors.Is(err, c.err) {
			t.Errorf("%v: got %v, want %v", c.dims, err, c.err)
		}
	}
}

func TestTimeIsOneMoreAxis(t *testing.T) {
	board, _ := New(8, 6)
	h, err := board.WithTime(30)
	if err != nil || h.Rank() != 3 || h.Dims()[2] != 30 {
		t.Fatalf("got %v %v", h.Dims(), err)
	}
	v, _ := New(4, 4, 4)
	vt, _ := v.WithTime(10)
	if _, err := vt.WithTime(2); !errors.Is(err, ErrRank) {
		t.Fatal("rank 5 accepted")
	}
}

func TestPathLengthIsManhattan(t *testing.T) {
	a, b := C(1, 5, 2), C(4, 2, 0)
	p := Path(a, b)
	if len(p) != a.Manhattan(b)+1 || p[0] != a || p[len(p)-1] != b {
		t.Fatalf("bad path %v", p)
	}
	for i := 1; i < len(p); i++ {
		if p[i-1].Manhattan(p[i]) != 1 {
			t.Fatalf("jump at %d", i)
		}
	}
}

func TestNearestFreeIsDeterministic(t *testing.T) {
	s, _ := New(5, 5)
	center := C(2, 2)
	taken := map[Coord]bool{center: true}
	isTaken := func(c Coord) bool { return taken[c] }
	any := func(Coord) bool { return true }
	if got, _ := s.NearestFree([]Coord{center}, isTaken, any); got != C(3, 2) {
		t.Fatalf("first free = %v", got)
	}
	taken[C(3, 2)] = true
	if got, _ := s.NearestFree([]Coord{center}, isTaken, any); got != C(1, 2) {
		t.Fatalf("second free = %v", got)
	}
	top := func(c Coord) bool { return c[1] == 0 }
	if got, _ := s.NearestFree([]Coord{center}, isTaken, top); got != C(2, 0) {
		t.Fatalf("rule = %v", got)
	}
	line, _ := New(1, 1, 3)
	if got, _ := line.NearestFree([]Coord{C(0, 0, 0)}, func(Coord) bool { return false }, any); got != C(0, 0, 1) {
		t.Fatalf("3D = %v", got)
	}
}

func TestAroundIsTheMooreNeighbourhood(t *testing.T) {
	for _, c := range []struct {
		dims []int
		want int
	}{{[]int{5}, 2}, {[]int{5, 5}, 8}, {[]int{5, 5, 5}, 26}, {[]int{3, 3, 3, 3}, 80}} {
		s, _ := New(c.dims...)
		if got := len(s.Around(s.Center())); got != c.want {
			t.Errorf("%v: %d cells around the center, want %d", c.dims, got, c.want)
		}
	}
}
