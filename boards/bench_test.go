package boards

import (
	"context"
	"fmt"
	"testing"
)

// BenchmarkAppendLine appends one line to a board of about 100 lines, the
// way the studio draws while the model writes.
func BenchmarkAppendLine(b *testing.B) {
	ctx := context.Background()
	s := NewService(NewMemStore(), "")
	src := "proc hub \"Hub\"\nproc db \"DB\"\n"
	for i := range 98 {
		src += fmt.Sprintf("hub -> db : query %d\n", i)
	}
	if _, err := s.Write(ctx, "b", src); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := range b.N {
		if _, err := s.Append(ctx, "b", fmt.Sprintf("db = busy%d", i%3)); err != nil {
			b.Fatal(err)
		}
		if i%50 == 49 {
			b.StopTimer()
			_, _ = s.Write(ctx, "b", src)
			b.StartTimer()
		}
	}
}
