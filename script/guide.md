# Board scripts

A board is a small world of **processes** (computers) that talk only by **messages**. You write what happens, line by line, and the board places everything, animates it, and draws the same run as a living world, a sequence diagram, or a timeline. You never write coordinates for anything but the world size.

## Statements

```
# comment
world 24 12                        optional; 1 to 4 sizes (a line, a board, voxels, ...)
kind db color=green states=idle,busy,down     optional; a type for processes
proc api "API gateway" kind=db color=red      start a process (label, kind, color optional)
proc api/cache                     a child of api, placed next to it
api -> db : SELECT cart            send a message; takes one tick
& api -> cache : get               "&" starts at the same time as the line above
db = busy                          change state (one word)
note db "slow disk"                a short note shown next to the process
body db 4                          give db a body of 4 cells (0 removes it)
wait 2                             let 2 ticks pass
end api/cache                      end a process and its children
cap db 1                           db handles at most 1 message per tick
on db SELECT: db -> $from : rows   rule: when db handles a message whose first word is SELECT
every 3: web -> api : GET          rule: repeat every 3 ticks from now
```

- Ids are names (`a-z A-Z 0-9 _ -`, starting with a letter), with `/` for children: `api/cache`.
- Colors: blue, red, green, amber, purple, teal, pink, gray. Leave them out and the viewer picks.
- If a kind lists states, its processes may only use those states.
- Rules run the actions after `:` (separated by `;`). Actions are messages, states, notes, bodies, `proc` and `end`. `$from` is the sender of the message being handled. `on db *:` matches any message.
- A rule belongs to a process: `on db ...` to db, `every 3: web -> ...` to web. Write it after that process starts; `end web` stops its rules.

## Time

Lines happen in order. A message advances time by one tick (it arrives on the next tick); `wait N` advances N ticks; everything else happens at the current tick. Rules keep running while time passes, so `every 1: ...` followed by `wait 20` plays 20 ticks of traffic.

## Bodies and backlog

A process's body is what `body` asks for plus every message waiting in its mailbox. With `cap`, a busy process visibly swells and drains. Bodies grow through free cells and never touch another process, so the board stays readable at any size.

## Tips for agents

- Start small and append: send a few lines, look at the errors, append more while you talk.
- Prefer a few meaningful processes over many. Use children for parts of one thing.
- Errors name the line and a stable code (`syntax`, `bad_id`, `unknown_proc`, `dup_proc`, `no_parent`, `unknown_state`, `bad_ref`, `too_long`, ...). A bad line is skipped; the rest of the board still runs.

## Example

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
