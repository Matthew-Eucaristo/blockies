package boards

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestAppendRejectsOnlyBadLines(t *testing.T) {
	ctx := context.Background()
	s := NewService(NewMemStore(), "http://x")
	if _, err := s.Write(ctx, "b", "proc a\nproc b\n"); err != nil {
		t.Fatal(err)
	}
	r, err := s.Append(ctx, "b", "a -> b : hi\na -> ghost : lost\nnonsense here\nb = busy")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Rejected) != 2 || r.Rejected[0].Line != 4 || r.Rejected[0].Code != "unknown_proc" || r.Rejected[1].Line != 5 || r.Rejected[1].Code != "syntax" {
		t.Fatalf("rejected = %+v", r.Rejected)
	}
	got, _ := s.Read(ctx, "b")
	if got.Script != "proc a\nproc b\na -> b : hi\nb = busy\n" || len(got.Errors) != 0 || got.Rev != 2 {
		t.Fatalf("script = %q, errors %v, rev %d", got.Script, got.Errors, got.Rev)
	}
	if r.URL != "http://x/b/b" {
		t.Fatal(r.URL)
	}
}

func TestSubscribersGetTheLatestFrame(t *testing.T) {
	ctx := context.Background()
	s := NewService(NewMemStore(), "")
	ch, cancel, err := s.Subscribe(ctx, "live")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	<-ch // the empty board
	for _, src := range []string{"proc a\n", "proc a\nproc b\n"} {
		if _, err := s.Write(ctx, "live", src); err != nil {
			t.Fatal(err)
		}
	}
	var f Frame
	if err := json.Unmarshal(<-ch, &f); err != nil {
		t.Fatal(err)
	}
	if f.Rev != 2 || len(f.Timeline.Procs) != 2 {
		t.Fatalf("frame rev %d with %d procs, want the latest", f.Rev, len(f.Timeline.Procs))
	}
	if err := s.Delete(ctx, "live"); err != nil {
		t.Fatal(err)
	}
	if _, ok := <-ch; ok {
		t.Fatal("channel still open after delete")
	}
	cancel() // must not panic after delete
}

func TestBadNamesAndMissingBoards(t *testing.T) {
	ctx := context.Background()
	s := NewService(NewMemStore(), "")
	var e *Error
	if _, err := s.Write(ctx, "Bad Name", ""); !errors.As(err, &e) || e.Code != "bad_board" {
		t.Fatal(err)
	}
	if _, err := s.Read(ctx, "nope"); !errors.As(err, &e) || e.Code != "not_found" {
		t.Fatal(err)
	}
	if _, err := s.Write(ctx, "big", strings.Repeat("#\n", MaxScript)); !errors.As(err, &e) || e.Code != "too_big" {
		t.Fatal(err)
	}
}

func TestDirStoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	st, err := NewDirStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for id, src := range map[string]string{"one": "proc a\n", "two": "proc b\n"} {
		if err := st.Put(ctx, id, src); err != nil {
			t.Fatal(err)
		}
	}
	ids, _ := st.List(ctx)
	if strings.Join(ids, ",") != "one,two" {
		t.Fatal(ids)
	}
	if v, _ := st.Get(ctx, "one"); v != "proc a\n" {
		t.Fatal(v)
	}
	if err := st.Delete(ctx, "one"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Get(ctx, "one"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}
