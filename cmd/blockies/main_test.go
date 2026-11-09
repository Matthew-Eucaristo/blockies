package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOfflineCommands(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.blk")
	bad := filepath.Join(dir, "bad.blk")
	for name, src := range map[string]string{good: "world 9\nproc a\nproc b\na -> b : hi\n", bad: "proc a\nnonsense\n"} {
		if err := os.WriteFile(name, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cases := []struct {
		args []string
		code int
		want string
	}{
		{[]string{"check", good}, exitOK, "2 processes, 1 messages"},
		{[]string{"check", bad}, exitBoardErrors, "bad.blk:2: syntax"},
		{[]string{"check", "--json", good}, exitOK, `"version": 1`},
		{[]string{"render", good}, exitOK, "A-->B"},
		{[]string{"guide"}, exitOK, "# Board scripts"},
		{[]string{"examples"}, exitOK, "checkout"},
		{[]string{"examples", "line"}, exitOK, "world 25"},
		{[]string{"write"}, exitUsage, ""},
		{[]string{"nope"}, exitUsage, ""},
		{[]string{"list", "--server", "http://127.0.0.1:1"}, exitNoServer, ""},
	}
	for _, c := range cases {
		var out, errOut bytes.Buffer
		code := run(c.args, strings.NewReader(""), &out, &errOut)
		if code != c.code || !strings.Contains(out.String(), c.want) {
			t.Errorf("%v: exit %d, want %d; output %q; stderr %q", c.args, code, c.code, out.String(), errOut.String())
		}
	}
}
