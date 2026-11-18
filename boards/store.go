package boards

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

// Store keeps board scripts. The script is the whole board: everything else
// is compiled from it, so a store never holds derived state.
type Store interface {
	Get(ctx context.Context, id string) (string, error) // ErrNotFound if missing
	Put(ctx context.Context, id, script string) error
	Delete(ctx context.Context, id string) error
	List(ctx context.Context) ([]string, error) // sorted ids
}

// ErrNotFound is returned by a Store for a missing board.
var ErrNotFound = errors.New("not found")

// MemStore keeps boards in memory only.
type MemStore struct {
	mu sync.Mutex
	m  map[string]string
}

// NewMemStore returns an empty in-memory store.
func NewMemStore() *MemStore { return &MemStore{m: map[string]string{}} }

func (s *MemStore) Get(_ context.Context, id string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[id]
	if !ok {
		return "", ErrNotFound
	}
	return v, nil
}

func (s *MemStore) Put(_ context.Context, id, script string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[id] = script
	return nil
}

func (s *MemStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, id)
	return nil
}

func (s *MemStore) List(context.Context) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]string, 0, len(s.m))
	for id := range s.m {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids, nil
}

// DirStore keeps one <id>.blk file per board, so boards are plain text that
// people and agents can also read, diff and commit.
type DirStore struct{ dir string }

// NewDirStore uses dir, creating it if needed.
func NewDirStore(dir string) (*DirStore, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &DirStore{dir: dir}, nil
}

func (s *DirStore) path(id string) string { return filepath.Join(s.dir, id+".blk") }

func (s *DirStore) Get(_ context.Context, id string) (string, error) {
	b, err := os.ReadFile(s.path(id))
	if errors.Is(err, fs.ErrNotExist) {
		return "", ErrNotFound
	}
	return string(b), err
}

// Put writes atomically: a temporary file, then a rename.
func (s *DirStore) Put(_ context.Context, id, script string) error {
	tmp, err := os.CreateTemp(s.dir, "."+id+"-*")
	if err != nil {
		return err
	}
	if _, err := tmp.WriteString(script); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), s.path(id))
}

func (s *DirStore) Delete(_ context.Context, id string) error {
	err := os.Remove(s.path(id))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

func (s *DirStore) List(context.Context) ([]string, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		if id, ok := strings.CutSuffix(e.Name(), ".blk"); ok && ValidID(id) {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	return ids, nil
}
