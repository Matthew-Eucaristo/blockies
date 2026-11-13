# Board scripts

A board is a small world of **processes** (computers) that talk only by **messages**. You write what happens, line by line, and the board places everything, animates it, and draws the same run as a living world, a sequence diagram, or a timeline. You never write coordinates for anything but the world size.

## Statements

```
# comment
world 24 12                        optional; 1 to 4 sizes (a line, a board, voxels, ...)
kind db color=green states=idle,busy,down     optional; a type for processes
proc api "API gateway" kind=db color=red      start a process (label, kind, color optional)
proc api/cache                     a child of api, placed next to it
proc qty "Qty" skin=slider min=1 max=50 value 3    a block people set (see Values)
api -> db : SELECT cart            send a message; takes one tick
& api -> cache : get               "&" starts at the same time as the line above
db = busy                          change state (one word)
note db "slow disk"                a short note shown next to the process
body db 4                          give db a body of 4 cells (0 removes it); body db +1 grows it by one
add tally 1                        add to tally's number (shown on the block); add tally -1 takes away
set total price * qty              set a value now (a number or "text")
calc total = price * qty           a formula: total always follows price and qty
wait 2                             let 2 ticks pass
end api/cache                      end a process and its children
cap db 1                           db handles at most 1 message per tick
on db SELECT: db -> $from : rows   rule: when db handles a message whose first word is SELECT
every 3: web -> api : GET          rule: repeat every 3 ticks from now
click vote/a: vote/a -> tally : A  rule: what happens when a person taps vote/a (it becomes a button)
focus stage                        every screen looks at stage and its children from now
```

- Ids are names (`a-z A-Z 0-9 _ -`, starting with a letter), with `/` for children: `api/cache`.
- Colors: blue, red, green, amber, purple, teal, pink, gray. Leave them out and the viewer picks.
- If a kind lists states, its processes may only use those states.
- Rules run the actions after `:` (separated by `;`). Actions are messages, states, notes, bodies, `add`, `focus`, `proc` and `end`. `$from` is the sender of the message being handled (in a click rule, the block that was tapped). `on db *:` matches any message.
- A rule belongs to a process: `on db ...` to db, `every 3: web -> ...` to web. Write it after that process starts; `end web` stops its rules.

## Values, formulas and controls

Every block can hold a value, a number or a "text", shown on the block. `skin=` says how a block looks and what people can do with it: `number`, `text`, `bar` (a gauge; `min=`/`max=`), and the controls `slider` (`min=`/`max=`), `input` (people type) and `toggle` (on/off). `value` gives the starting value.

Formulas use values by id: `+ - * / %`, comparisons, `and`, `or`, `not`, parentheses, `min max round floor ceil abs clamp if`, and `"text" + value`. Put spaces around `-` and `/` (`a-b` and `a/b` are ids). `calc` keeps a value up to date; `set` and `add` change one at a moment. `cap` and `body` take formulas too: `cap db capacity`. Numbers stay within 1e15 (`bad_number` past that), and a board computes at most 200000 formulas in one run.

When a person moves a slider, types into an input or flips a toggle, the server changes that block's starting `value`, and the whole board replays as if it had always been so: a what-if. Taps are the opposite: events added at the end.

```
proc price "Price" skin=input value 12000
proc qty "Qty" skin=slider min=1 max=50 value 3
proc total "Total" skin=number
calc total = price * qty
```

## People taking part

A board can be something people use, not only watch. `click` makes a block a button: when someone taps it (on the board page, or on a phone), the server writes `tap vote/a` at the end of the board and the click rule runs. Taps that come in together share a tick. Inside a click rule, `$from` is the block that was tapped.

Build the whole thing once (buttons, rules, a block that counts), then let people use it: you are not needed for every tap, and every screen replays the same run.

`focus` points every screen at one part of the board, like a presenter moving the slide. People can also open one part on their own (a link with `?view=part`), which is how a phone shows only the voting buttons.

```
proc stage "Which first?"
proc stage/a "A" color=amber
proc stage/b "B" color=teal
proc vote "Vote"
proc vote/a "Vote A" color=amber
proc vote/b "Vote B" color=teal
click vote/a: vote/a -> stage/a : vote
click vote/b: vote/b -> stage/b : vote
on stage/a vote: add stage/a 1; body stage/a +1
on stage/b vote: add stage/b 1; body stage/b +1
focus stage
```

Taps arrive as one line per half second, for example `tap vote/a 12 vote/b 5` (12 people tapped A, 5 tapped B; the click rule runs once per tap). A tap takes one tick, so keep a click's effect within one hop: put `add` and `body` in the click rule itself, or in an `on` rule of the block its message reaches.

`body x +1` grows what x asks for; messages waiting in x's mailbox still add to what you see.

Once people have tapped, prefer appending to redrawing: `redraw` (or `board_write`) replaces the whole script, taps included. Do not write `tap` lines yourself unless you are showing what would happen when people tap. People can also drag blocks; that writes `move id x y`. Agents still never say where: leave `move` and `at` to people.

## Time

Lines happen in order. A message advances time by one tick (it arrives on the next tick); `wait N` advances N ticks; everything else happens at the current tick. Rules keep running while time passes, so `every 1: ...` followed by `wait 20` plays 20 ticks of traffic.

## Bodies and backlog

A process's body is what `body` asks for plus every message waiting in its mailbox. With `cap`, a busy process visibly swells and drains. Bodies grow through free cells and never touch another process, so the board stays readable at any size.

## Tips for agents

- Start small and append: send a few lines, look at the errors, append more while you talk.
- Prefer a few meaningful processes over many. Use children for parts of one thing.
- Errors name the line and a stable code (`syntax`, `bad_id`, `unknown_proc`, `dup_proc`, `no_parent`, `unknown_state`, `bad_ref`, `no_click`, `too_long`, ...). A bad line is skipped; the rest of the board still runs.

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
