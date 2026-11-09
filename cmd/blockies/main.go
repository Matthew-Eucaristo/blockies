// Command blockies runs the board server, the MCP server for agents, and
// small commands for scripts and people.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Matthew-Eucaristo/blockies/boards"
	"github.com/Matthew-Eucaristo/blockies/client"
	"github.com/Matthew-Eucaristo/blockies/examples"
	"github.com/Matthew-Eucaristo/blockies/kernel"
	"github.com/Matthew-Eucaristo/blockies/mcpserver"
	"github.com/Matthew-Eucaristo/blockies/script"
	"github.com/Matthew-Eucaristo/blockies/server"
)

// Exit codes are stable for scripts and agents.
const (
	exitOK          = 0
	exitBoardErrors = 1 // the board has errors, or lines were rejected
	exitUsage       = 2
	exitAPI         = 3 // the server refused (not_found, bad_board, ...)
	exitNoServer    = 4
)

const usage = `blockies: live boards that agents write and people watch.

Server:
  blockies serve [--addr 127.0.0.1:4318] [--dir DIR | --memory]
  blockies mcp   [--server URL]          MCP over stdio (starts a server if none is running)

Boards (talk to the server; add --json for machine output):
  blockies write  BOARD [FILE]           replace a board's script (FILE or stdin)
  blockies append BOARD [LINE...]        add lines (arguments or stdin)
  blockies read   BOARD                  print the script and its errors
  blockies list                          list boards
  blockies rm     BOARD                  delete a board
  blockies open   BOARD                  open the board in a browser
  blockies demo                          load the example boards

Offline:
  blockies check  FILE...                compile and report errors (--json: the timeline)
  blockies render FILE [--tick N]        draw a board as text
  blockies guide                         the board language
  blockies examples [NAME]               list or print the examples
  blockies version

Environment: BLOCKIES_SERVER (default http://127.0.0.1:4318), BLOCKIES_DIR (default ~/.blockies/boards).
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Fprint(stdout, usage)
		return exitOK
	}
	cmd, args := args[0], args[1:]
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "machine-readable output")
	serverURL := fs.String("server", env("BLOCKIES_SERVER", "http://127.0.0.1:4318"), "board server URL")
	addr := fs.String("addr", env("BLOCKIES_ADDR", "127.0.0.1:4318"), "listen address")
	dir := fs.String("dir", env("BLOCKIES_DIR", defaultDir()), "directory for board files")
	memory := fs.Bool("memory", false, "keep boards in memory only")
	public := fs.String("url", "", "public base URL for links (default http://ADDR)")
	tick := fs.Int("tick", -1, "tick to render (default: the last)")
	webDir := fs.String("web", os.Getenv("BLOCKIES_WEB"), "serve the viewer from this directory (for viewer development)")
	pos, err := parse(fs, args)
	if err != nil {
		return exitUsage
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	out := &printer{w: stdout, asJSON: *asJSON}
	api := client.New(*serverURL)

	switch cmd {
	case "serve":
		return serve(ctx, *addr, *dir, *memory, *public, *webDir, stderr)
	case "mcp":
		return serveMCP(ctx, api, *addr, *dir, *public, stdin, stdout, stderr)
	case "write":
		if len(pos) < 1 || len(pos) > 2 {
			return bad(stderr, "write BOARD [FILE]")
		}
		src, err := readSource(pos[1:], stdin)
		if err != nil {
			return fail(stderr, err)
		}
		r, err := api.Write(ctx, pos[0], src)
		return out.result(r, err, stderr)
	case "append":
		if len(pos) < 1 {
			return bad(stderr, "append BOARD [LINE...]")
		}
		lines := strings.Join(pos[1:], "\n")
		if len(pos) == 1 {
			b, err := io.ReadAll(stdin)
			if err != nil {
				return fail(stderr, err)
			}
			lines = string(b)
		}
		r, err := api.Append(ctx, pos[0], lines)
		return out.result(r, err, stderr)
	case "read":
		if len(pos) != 1 {
			return bad(stderr, "read BOARD")
		}
		r, err := api.Read(ctx, pos[0])
		if err == nil && !*asJSON {
			fmt.Fprint(stdout, r.Script)
			fmt.Fprintln(stdout, "#", r.Summary, "·", r.URL)
		}
		return out.result(r, err, stderr)
	case "list":
		rs, err := api.List(ctx)
		if err != nil {
			return fail(stderr, err)
		}
		if *asJSON {
			return out.json(map[string]any{"boards": rs})
		}
		for _, r := range rs {
			fmt.Fprintf(stdout, "%-24s %s  %s\n", r.Board, r.Summary, r.URL)
		}
		return exitOK
	case "rm":
		if len(pos) != 1 {
			return bad(stderr, "rm BOARD")
		}
		if err := api.Delete(ctx, pos[0]); err != nil {
			return fail(stderr, err)
		}
		return exitOK
	case "open":
		if len(pos) != 1 {
			return bad(stderr, "open BOARD")
		}
		u := strings.TrimRight(*serverURL, "/") + "/b/" + pos[0]
		fmt.Fprintln(stdout, u)
		openBrowser(u)
		return exitOK
	case "demo":
		code := exitOK
		for _, name := range examples.Names() {
			src, _ := examples.Get(name)
			r, err := api.Write(ctx, name, src)
			if c := out.result(r, err, stderr); c != exitOK {
				code = c
			}
		}
		return code
	case "check":
		if len(pos) == 0 {
			return bad(stderr, "check FILE...")
		}
		return check(pos, out, stdout, stderr)
	case "render":
		if len(pos) != 1 {
			return bad(stderr, "render FILE [--tick N]")
		}
		src, err := readSource(pos, stdin)
		if err != nil {
			return fail(stderr, err)
		}
		tl := kernel.Compile(src)
		fmt.Fprint(stdout, render(tl, *tick))
		if len(tl.Errors) > 0 {
			return exitBoardErrors
		}
		return exitOK
	case "guide":
		fmt.Fprint(stdout, script.Guide)
		return exitOK
	case "examples":
		if len(pos) == 0 {
			fmt.Fprintln(stdout, strings.Join(examples.Names(), "\n"))
			return exitOK
		}
		src, ok := examples.Get(pos[0])
		if !ok {
			return bad(stderr, "examples NAME, one of: "+strings.Join(examples.Names(), ", "))
		}
		fmt.Fprint(stdout, src)
		return exitOK
	case "version":
		fmt.Fprintln(stdout, version())
		return exitOK
	}
	fmt.Fprintf(stderr, "unknown command %q\n\n%s", cmd, usage)
	return exitUsage
}

// parse lets flags and arguments come in any order, so `write b f --json`
// works like `write --json b f`.
func parse(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return pos, nil
		}
		pos = append(pos, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

func serve(ctx context.Context, addr, dir string, memory bool, public, webDir string, stderr io.Writer) int {
	log := slog.New(slog.NewTextHandler(stderr, nil))
	svc, err := newService(addr, dir, memory, public)
	if err != nil {
		return fail(stderr, err)
	}
	ms := mcpserver.New(svc, version())
	opts := server.Options{MCP: ms, Log: log}
	if webDir != "" {
		opts.Web = os.DirFS(webDir)
	}
	srv := server.NewHTTPServer(addr, server.Handler(svc, opts))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fail(stderr, err)
	}
	where := "memory"
	if !memory {
		where = dir
	}
	log.Info("serving boards", "url", "http://"+ln.Addr().String(), "mcp", "http://"+ln.Addr().String()+"/mcp", "boards", where)
	go func() {
		<-ctx.Done()
		shut, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shut)
	}()
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fail(stderr, err)
	}
	return exitOK
}

// serveMCP speaks MCP on stdin and stdout. If a board server is already
// running it becomes a client of it; otherwise it starts one in this process
// so the links it returns work. Logs go to stderr: stdout is the protocol.
func serveMCP(ctx context.Context, remote *client.Client, addr, dir, public string, stdin io.Reader, stdout, stderr io.Writer) int {
	log := slog.New(slog.NewTextHandler(stderr, nil))
	var api boards.API = remote
	if !remote.Ping(ctx) {
		svc, err := newService(addr, dir, false, public)
		if err != nil {
			return fail(stderr, err)
		}
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			fmt.Fprintf(stderr, "no board server at %s and cannot start one on %s: %v\n", remote.Base, addr, err)
			return exitNoServer
		}
		srv := server.NewHTTPServer(addr, server.Handler(svc, server.Options{Log: log}))
		go func() { _ = srv.Serve(ln) }()
		defer func() { _ = srv.Close() }()
		log.Info("started a board server for this session", "url", "http://"+ln.Addr().String(), "boards", dir)
		api = svc
	}
	s := mcpserver.New(api, version())
	t := &mcp.IOTransport{Reader: io.NopCloser(stdin), Writer: nopWriteCloser{stdout}}
	if err := s.Run(ctx, t); err != nil && !errors.Is(err, context.Canceled) {
		log.Error("mcp", "err", err)
		return exitAPI
	}
	return exitOK
}

func newService(addr, dir string, memory bool, public string) (*boards.Service, error) {
	if public == "" {
		public = "http://" + addr
	}
	var store boards.Store = boards.NewMemStore()
	if !memory {
		ds, err := boards.NewDirStore(dir)
		if err != nil {
			return nil, err
		}
		store = ds
	}
	return boards.NewService(store, public), nil
}

func check(files []string, out *printer, stdout, stderr io.Writer) int {
	code := exitOK
	for _, f := range files {
		src, err := readSource([]string{f}, nil)
		if err != nil {
			return fail(stderr, err)
		}
		tl := kernel.Compile(src)
		if len(tl.Errors) > 0 {
			code = exitBoardErrors
		}
		if out.asJSON {
			out.json(tl)
			continue
		}
		fmt.Fprintf(stdout, "%s: %s\n", f, tl.Summary())
		for _, e := range tl.Errors {
			fmt.Fprintf(stdout, "  %s:%d: %s: %s\n", f, e.Line, e.Code, e.Detail)
		}
	}
	return code
}

type printer struct {
	w      io.Writer
	asJSON bool
}

func (p *printer) json(v any) int {
	enc := json.NewEncoder(p.w)
	enc.SetIndent("", "  ")
	enc.Encode(v)
	return exitOK
}

// result prints a board result and turns it into an exit code.
func (p *printer) result(r boards.Result, err error, stderr io.Writer) int {
	if err != nil {
		return fail(stderr, err)
	}
	if p.asJSON {
		p.json(r)
	} else {
		fmt.Fprintf(p.w, "%s rev %d: %s\n%s\n", r.Board, r.Rev, r.Summary, r.URL)
		for _, e := range r.Rejected {
			fmt.Fprintf(p.w, "  rejected line %d: %s: %s\n", e.Line, e.Code, e.Detail)
		}
		for _, e := range r.Errors {
			fmt.Fprintf(p.w, "  line %d: %s: %s\n", e.Line, e.Code, e.Detail)
		}
	}
	if len(r.Errors) > 0 || len(r.Rejected) > 0 {
		return exitBoardErrors
	}
	return exitOK
}

func fail(stderr io.Writer, err error) int {
	var e *boards.Error
	switch {
	case errors.As(err, &e):
		fmt.Fprintf(stderr, "%s: %s\n", e.Code, e.Detail)
		return exitAPI
	case errors.Is(err, client.ErrUnreachable):
		fmt.Fprintf(stderr, "%v\nStart one with: blockies serve\n", err)
		return exitNoServer
	}
	fmt.Fprintln(stderr, err)
	return exitAPI
}

func bad(stderr io.Writer, use string) int {
	fmt.Fprintf(stderr, "usage: blockies %s\n", use)
	return exitUsage
}

func readSource(args []string, stdin io.Reader) (string, error) {
	if len(args) == 0 || args[0] == "-" {
		if stdin == nil {
			return "", errors.New("no input")
		}
		b, err := io.ReadAll(io.LimitReader(stdin, boards.MaxScript+1))
		return string(b), err
	}
	b, err := os.ReadFile(args[0])
	return string(b), err
}

func openBrowser(u string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", u)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", u)
	default:
		cmd = exec.Command("xdg-open", u)
	}
	_ = cmd.Start()
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func defaultDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "boards"
	}
	return filepath.Join(home, ".blockies", "boards")
}

func version() string {
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return "dev"
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }
