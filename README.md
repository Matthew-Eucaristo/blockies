# Blockies

**Live boards that agents write and people watch.** When an AI agent wants to show you what it means (a system, a request flow, a plan, its own work), it writes a few lines, gives you a link, and keeps adding lines while it talks. You watch the board come alive.

A board is a small world of **processes** that talk only by **messages**. The agent says *what* happens and never *where*: the kernel places every process, grows bodies around them, and keeps them readable. The same run is shown three ways:

- **World:** processes on a lattice, messages flying between them, backlogs swelling into visible bodies.
- **Voxel:** the same world in 3D (worlds can have 1 to 4 axes).
- **Sequence:** time as an axis, so the run becomes a sequence diagram and a timeline. Nobody draws it.

> Status: early. Working name. Go core, server, MCP and web viewer work; hosting and accounts come later.

## Try it

You need Go 1.27 or newer.

```bash
go install github.com/Matthew-Eucaristo/blockies/cmd/blockies@latest
```

Start the server:

```bash
blockies serve
```

In another shell, load the examples and open one:

```bash
blockies demo
```

```bash
blockies open checkout
```

## Connect an agent

Claude Code (or any MCP client):

```bash
claude mcp add blockies -- blockies mcp
```

Then ask it to show you something, for example "show me how a login request flows through our services". It gets the language through the MCP server and replies with a link. `blockies mcp` uses the running server, or starts one for the session.

Without MCP, any agent with a shell can do the same:

```bash
blockies append demo 'proc api "API"' 'proc db "Postgres"' 'api -> db : SELECT user'
```

## The language

```
kind store color=green states=idle,busy
proc browser "Browser"
proc api "API"
proc db "Postgres" kind=store
browser -> api : GET /cart
api -> db : SELECT cart
db = busy
wait 1
db -> api : 3 rows
db = idle
api -> browser : 200 OK
```

Messages take one tick. `&` runs a line at the same time as the one above. Rules make boards run by themselves: `on db SELECT: db -> $from : rows`, `every 2: web -> api : GET`. `cap db 1` limits how fast db works, so its mailbox grows into a body you can see. The full reference is `blockies guide` (also at `/api/guide`).

## Layout

| Path | What |
|---|---|
| `lattice/` | A grid that never knows its dimension (rank 1 to 4) |
| `script/` | The board language: parser, formatter, guide for agents |
| `kernel/` | Runs a script into a timeline: placement, messages, rules, bodies. Pure and deterministic |
| `boards/` | The one place boards change: store, compile, fan out to viewers |
| `server/`, `mcpserver/`, `client/` | HTTP and event stream, MCP tools, HTTP client |
| `web/` | The viewer: plain ES modules, embedded in the binary |
| `cmd/blockies/` | The binary |
| `examples/` | Example boards |
| `explorations/` | The eight concept sketches this design came from |

More: [docs/architecture.md](docs/architecture.md), [docs/decisions.md](docs/decisions.md).

Co-created by Matthew and Edward Manhattan.
