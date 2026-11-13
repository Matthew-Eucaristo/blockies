package boards

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
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

func TestEvictionNeverLosesWrites(t *testing.T) {
	ctx := context.Background()
	s := NewService(NewMemStore(), "")
	var wg sync.WaitGroup
	for w := range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 50 {
				if _, err := s.Append(ctx, "hot", fmt.Sprintf("proc p%d_%d", w, i)); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	// Fill the cache past its limit over and over, so "hot" keeps being a
	// candidate for eviction while it is written.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := range 2 * maxCached {
			_, _ = s.Write(ctx, fmt.Sprintf("cold%d", i), "proc a\n")
		}
	}()
	wg.Wait()
	r, err := s.Read(ctx, "hot")
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(r.Script, "\n"); n != 200 {
		t.Fatalf("hot has %d lines, want 200", n)
	}
	s.mu.Lock()
	n := len(s.boards)
	s.mu.Unlock()
	if n > maxCached+1 {
		t.Fatalf("cache holds %d boards", n)
	}
}

func TestAppendThatBreaksAnOldLineIsRejected(t *testing.T) {
	ctx := context.Background()
	s := NewService(NewMemStore(), "")
	// Rules on existing lines that start failing because of new lines are
	// the new lines' fault; here the busy rules are old and the waits new.
	rule := "every 1: a = x" + strings.Repeat("; a = y", 54)
	if _, err := s.Write(ctx, "b", "proc a\n"+strings.Repeat(rule+"\n", 30)); err != nil {
		t.Fatal(err)
	}
	before, _ := s.Read(ctx, "b")
	r, err := s.Append(ctx, "b", strings.Repeat("wait 499\n", 4))
	if err != nil {
		t.Fatal(err)
	}
	after, _ := s.Read(ctx, "b")
	if len(r.Rejected) != 1 || r.Rejected[0].Code != "too_big" || after.Script != before.Script {
		t.Fatalf("rejected %+v, script changed: %v", r.Rejected, after.Script != before.Script)
	}
}

func TestSubscriberLimit(t *testing.T) {
	ctx := context.Background()
	s := NewService(NewMemStore(), "")
	var cancels []func()
	for range maxSubscribers {
		_, cancel, err := s.Subscribe(ctx, "b")
		if err != nil {
			t.Fatal(err)
		}
		cancels = append(cancels, cancel)
	}
	var e *Error
	if _, _, err := s.Subscribe(ctx, "b"); !errors.As(err, &e) || e.Code != "busy" {
		t.Fatalf("err = %v", err)
	}
	cancels[0]()
	if _, cancel, err := s.Subscribe(ctx, "b"); err != nil {
		t.Fatal(err)
	} else {
		cancel()
	}
}

func TestAppendThatKeepsHittingABudgetIsRejectedWhole(t *testing.T) {
	ctx := context.Background()
	s := NewService(NewMemStore(), "")
	if _, err := s.Write(ctx, "b", "proc a\n"); err != nil {
		t.Fatal(err)
	}
	rule := "every 1: a = x" + strings.Repeat("; a = y", 54)
	r, err := s.Append(ctx, "b", strings.Repeat(rule+"\n", 40)+strings.Repeat("wait 499\n", 4))
	if err != nil {
		t.Fatal(err)
	}
	after, _ := s.Read(ctx, "b")
	if after.Script != "proc a\n" || len(after.Errors) != 0 || len(r.Rejected) == 0 {
		t.Fatalf("partly committed: %d lines, errors %v, rejected %d", strings.Count(after.Script, "\n"), after.Errors, len(r.Rejected))
	}
}

func TestRevNeverGoesBackAfterEviction(t *testing.T) {
	ctx := context.Background()
	s := NewService(NewMemStore(), "")
	for range 3 {
		if _, err := s.Append(ctx, "keep", "proc a"); err != nil {
			t.Fatal(err)
		}
	}
	for i := range maxCached + 10 {
		_, _ = s.Write(ctx, fmt.Sprintf("other%d", i), "proc a\n")
	}
	r, _ := s.Read(ctx, "keep")
	if r.Rev < 1 {
		t.Fatalf("rev went back to %d", r.Rev)
	}
}
