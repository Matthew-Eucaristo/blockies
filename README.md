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

## Talk to it

Every board is something you can talk to. Hold Space (or the microphone), say what you want to see, and an agent draws it while it answers aloud, in English or Indonesian. Say "what if the database only handles one query a second?" and watch the queue grow. Press `?` for every shortcut; Share copies a watch-only link.

It needs Anthropic credentials on the server:

```bash
ANTHROPIC_API_KEY=... blockies serve
```

Or keep the key in a `.env` file in the repo (git ignores it) and load it into the shell first; the binary itself does not read `.env`:

```bash
set -a; . ./.env; set +a; blockies serve
```

```bash
blockies open ideas
```

Speech recognition runs in the browser (Chrome and Safari; Firefox and Edge are not reliable yet). Elsewhere, type, or use dictation (on a Mac, press fn twice) in the text box. `--model` and `--effort` pick the model (default `claude-opus-5-5`, low effort for quick replies).

The server listens on 127.0.0.1 and only answers to its own host names, so other web pages cannot write to your boards. A watch link is not a password: anyone who can reach the server can open any board. To use it from a phone on your network, start it with `--addr 0.0.0.0:4318 --url http://YOUR-IP:4318`; the microphone needs HTTPS there (browsers only allow it on localhost or HTTPS), so type or use the phone's dictation.

## Let people take part

Blocks can be buttons. Ask the agent for "a vote between coffee and tea for my talk" (or load `blockies demo` and open the `vote` board): the big screen shows the result, and people open the voting part on their phones (select it, then Menu → Copy a phone link; the link ends in `?view=vote`) and tap. Every tap is a line on the board, so every screen shows the same run. Phones on your network need the server started with `--addr 0.0.0.0:4318 --url http://YOUR-IP:4318`.

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
| `agent/` | The studio's harness: Claude draws on a board while it answers |
| `web/` | The board page: plain ES modules with registries for views, actions and panels, embedded in the binary |
| `cmd/blockies/` | The binary |
| `examples/` | Example boards |
| `explorations/` | The eight concept sketches this design came from |

More: [docs/architecture.md](docs/architecture.md), [docs/decisions.md](docs/decisions.md).

Co-created by Matthew and Edward Manhattan.
