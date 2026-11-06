//! The line DSL agents and humans write. One statement per line; a bad line is reported and
//! skipped, every valid line still loads.
//!
//! ```text
//! box api "API" at 720 220 states idle|busy|overloaded
//! arrow a1 browser -> api states off|on
//! token req "GET /" on browser, api, browser
//! link load -> cpu, api
//! step "API is busy" api=busy req=1
//! sim 512 256 scene calm seed 7
//! ```

use blockies_model::{Assign, Board, Kind, Link, Piece, Point, Scene, SimSpec, Step};
use std::fmt::Write as _;

/// A problem on one line (1-based), in words an agent can act on.
#[derive(Clone, Debug, PartialEq, Eq, serde::Serialize, serde::Deserialize)]
pub struct LineError {
    pub line: usize,
    pub message: String,
}

#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct Parsed {
    pub board: Board,
    pub errors: Vec<LineError>,
}

#[derive(Clone, Debug, PartialEq, Eq)]
enum Tok {
    Word(String),
    Text(String),
}

impl Tok {
    fn word(&self) -> Option<&str> {
        match self {
            Self::Word(w) => Some(w),
            Self::Text(_) => None,
        }
    }

    fn show(&self) -> &str {
        match self {
            Self::Word(w) | Self::Text(w) => w,
        }
    }
}

fn tokenize(line: &str) -> Result<Vec<Tok>, String> {
    let mut out = Vec::new();
    let mut chars = line.chars().peekable();
    while let Some(&c) = chars.peek() {
        if c.is_whitespace() {
            chars.next();
        } else if c == '"' {
            chars.next();
            let mut s = String::new();
            loop {
                match chars.next() {
                    None => return Err("unclosed \"quote\"".into()),
                    Some('"') => break,
                    Some('\\') => match chars.next() {
                        Some('n') => s.push('\n'),
                        Some(other) => s.push(other),
                        None => return Err("unclosed \"quote\"".into()),
                    },
                    Some(ch) => s.push(ch),
                }
            }
            out.push(Tok::Text(s));
        } else {
            let mut w = String::new();
            while let Some(&ch) = chars.peek() {
                if ch.is_whitespace() || ch == '"' {
                    break;
                }
                w.push(ch);
                chars.next();
            }
            out.push(Tok::Word(w));
        }
    }
    Ok(out)
}

const KEYS: [&str; 8] = [
    "at", "size", "states", "values", "on", "back", "state", "offset",
];

fn is_id(s: &str) -> bool {
    let mut c = s.chars();
    c.next()
        .is_some_and(|f| f.is_ascii_alphabetic() || f == '_')
        && c.all(|ch| ch.is_ascii_alphanumeric() || ch == '_' || ch == '-')
}

fn num(t: Option<&Tok>) -> Result<i32, String> {
    let t = t.ok_or("expected a number, got end of line")?;
    t.word()
        .and_then(|w| w.parse::<f64>().ok())
        .filter(|v| v.is_finite() && v.abs() < 1e7)
        .map(|v| v.round() as i32)
        .ok_or_else(|| format!("expected a number, got \"{}\"", t.show()))
}

fn word<'a>(t: Option<&'a Tok>, what: &str) -> Result<&'a str, String> {
    t.and_then(Tok::word)
        .ok_or_else(|| format!("expected {what}"))
}

/// Splits `a, b c,d` into ids.
fn id_list<'a>(words: impl Iterator<Item = &'a str>) -> Vec<String> {
    words
        .flat_map(|w| w.split(','))
        .filter(|s| !s.is_empty())
        .map(str::to_string)
        .collect()
}

/// A step before its ids and states are checked: caption, `(id, state)` pairs, line.
type RawStep = (String, Vec<(String, String)>, usize);

struct Pending {
    piece: Piece,
    line: usize,
    init: Option<String>,
}

pub fn parse(text: &str) -> Parsed {
    let mut errors = Vec::new();
    let mut pieces: Vec<Pending> = Vec::new();
    let mut links: Vec<(Link, usize)> = Vec::new();
    let mut steps: Vec<RawStep> = Vec::new();
    let mut sim: Option<SimSpec> = None;

    for (i, raw) in text.lines().enumerate() {
        let line = raw.trim();
        if line.is_empty() || line.starts_with('#') || line.starts_with("```") {
            continue;
        }
        let res = tokenize(line).and_then(|tk| {
            let head = tk[0].show().to_ascii_lowercase();
            match head.as_str() {
                "link" => parse_link(&tk).map(|l| links.push((l, i))),
                "step" => parse_step(&tk).map(|(c, s)| steps.push((c, s, i))),
                "sim" => {
                    if sim.is_some() {
                        return Err("only one sim per board".into());
                    }
                    sim = Some(parse_sim(&tk)?);
                    Ok(())
                }
                _ => {
                    let p = parse_piece(&head, &tk)?;
                    if pieces.iter().any(|q| q.piece.id == p.piece.id) {
                        return Err(format!("duplicate id \"{}\"", p.piece.id));
                    }
                    pieces.push(Pending { line: i, ..p });
                    Ok(())
                }
            }
        });
        if let Err(message) = res {
            errors.push(LineError {
                line: i + 1,
                message,
            });
        }
    }

    // Second pass: references between pieces.
    let exists =
        |id: &str, ps: &[Pending]| ps.iter().find(|q| q.piece.id == id).map(|q| q.piece.kind);
    let mut kept = Vec::with_capacity(pieces.len());
    for p in &pieces {
        let err = match p.piece.kind {
            Kind::Arrow => {
                let ok = [&p.piece.from, &p.piece.to]
                    .iter()
                    .all(|e| e.as_deref().is_some_and(|id| exists(id, &pieces).is_some()));
                (!ok).then(|| format!("arrow \"{}\" points to an unknown id", p.piece.id))
            }
            Kind::Token => p
                .piece
                .route
                .iter()
                .find(|id| !matches!(exists(id, &pieces), Some(k) if k != Kind::Token))
                .map(|bad| format!("token route has unknown id \"{bad}\"")),
            _ => None,
        };
        match err {
            Some(message) => errors.push(LineError {
                line: p.line + 1,
                message,
            }),
            None => kept.push(p),
        }
    }
    let mut board = Board {
        pieces: Vec::with_capacity(kept.len()),
        sim,
        ..Board::default()
    };
    for p in &kept {
        let mut piece = p.piece.clone();
        if let Some(init) = &p.init {
            match piece.resolve_state(init) {
                Some(k) => piece.base = k,
                None => errors.push(LineError {
                    line: p.line + 1,
                    message: format!(
                        "\"{}\" has no state \"{init}\" (has {})",
                        piece.id,
                        piece.states.join("|")
                    ),
                }),
            }
        }
        board.pieces.push(piece);
    }
    for (l, line) in links {
        match std::iter::once(&l.from)
            .chain(&l.to)
            .find(|id| board.index(id).is_none())
        {
            Some(bad) => errors.push(LineError {
                line: line + 1,
                message: format!("link uses unknown id \"{bad}\""),
            }),
            None => board.links.push(l),
        }
    }
    let driven = board.driven();
    for (caption, set, line) in steps {
        let mut out = Vec::new();
        for (id, state) in set {
            let msg = match board.index(&id) {
                None => Some(format!("step uses unknown id \"{id}\"")),
                Some(k) if driven[k] => {
                    Some(format!("\"{id}\" follows a link; set its source instead"))
                }
                Some(k) => match board.pieces[k].resolve_state(&state) {
                    Some(s) => {
                        out.push(Assign {
                            piece: id,
                            state: s,
                        });
                        None
                    }
                    None => Some(format!(
                        "\"{id}\" has no state \"{state}\" (has {})",
                        board.pieces[k].states.join("|")
                    )),
                },
            };
            if let Some(message) = msg {
                errors.push(LineError {
                    line: line + 1,
                    message,
                });
            }
        }
        board.steps.push(Step { caption, set: out });
    }
    errors.sort_by_key(|e| e.line);
    Parsed { board, errors }
}

fn parse_link(tk: &[Tok]) -> Result<Link, String> {
    if tk.get(2).and_then(Tok::word) != Some("->") {
        return Err("use: link source -> target, target".into());
    }
    let from = word(tk.get(1), "a source id")?.to_string();
    let to = id_list(tk[3..].iter().filter_map(Tok::word));
    if to.is_empty() {
        return Err("link needs at least one target".into());
    }
    Ok(Link { from, to })
}

fn parse_step(tk: &[Tok]) -> Result<(String, Vec<(String, String)>), String> {
    let (caption, rest) = match tk.get(1) {
        Some(Tok::Text(c)) => (c.clone(), &tk[2..]),
        _ => (String::new(), &tk[1..]),
    };
    let set = rest
        .iter()
        .map(|t| match t.word().and_then(|w| w.split_once('=')) {
            Some((id, state)) if !id.is_empty() && !state.is_empty() => {
                Ok((id.to_string(), state.to_string()))
            }
            _ => Err(format!("step expects id=state, got \"{}\"", t.show())),
        })
        .collect::<Result<_, _>>()?;
    Ok((caption, set))
}

fn parse_sim(tk: &[Tok]) -> Result<SimSpec, String> {
    let dim = |t: Option<&Tok>| -> Result<u32, String> {
        let v = num(t)?;
        if (128..=8192).contains(&v) && v % 64 == 0 {
            Ok(v as u32)
        } else {
            Err(format!(
                "sim size {v} must be a multiple of 64 between 128 and 8192"
            ))
        }
    };
    let mut spec = SimSpec {
        width: dim(tk.get(1))?,
        height: dim(tk.get(2))?,
        scene: Scene::Empty,
        seed: 1,
    };
    let mut j = 3;
    while j < tk.len() {
        match tk[j].show() {
            "scene" => {
                let s = word(tk.get(j + 1), "a scene (empty, calm, storm)")?;
                spec.scene = Scene::parse(s)
                    .ok_or_else(|| format!("unknown scene \"{s}\" (empty, calm, storm)"))?;
            }
            "seed" => {
                spec.seed = u32::try_from(num(tk.get(j + 1))?)
                    .map_err(|_| "seed must be >= 0".to_string())?
            }
            other => {
                return Err(format!(
                    "unexpected \"{other}\" (use: sim W H [scene S] [seed N])"
                ));
            }
        }
        j += 2;
    }
    Ok(spec)
}

fn parse_piece(head: &str, tk: &[Tok]) -> Result<Pending, String> {
    let kind = Kind::parse(head).ok_or_else(|| {
        let names: Vec<_> = Kind::ALL.iter().map(|k| k.name()).collect();
        format!(
            "unknown piece \"{head}\" (allowed: {}, link, step, sim)",
            names.join(", ")
        )
    })?;
    let id = tk
        .get(1)
        .and_then(Tok::word)
        .filter(|w| is_id(w))
        .ok_or_else(|| format!("{head} needs an id"))?;
    let mut p = Piece::new(id, kind);
    let mut label = None;
    let mut init = None;
    let mut states: Option<Vec<String>> = None;
    let mut j = 2;
    if kind == Kind::Arrow {
        if tk.get(3).and_then(Tok::word) != Some("->") {
            return Err("use: arrow id from -> to".into());
        }
        p.from = Some(word(tk.get(2), "a source id")?.to_string());
        p.to = Some(word(tk.get(4), "a target id")?.to_string());
        j = 5;
    }
    while j < tk.len() {
        let t = &tk[j];
        if let Tok::Text(s) = t {
            if label.is_some() {
                return Err(format!("extra text \"{s}\""));
            }
            label = Some(s.clone());
            j += 1;
            continue;
        }
        match t.show() {
            "at" => {
                p.at = Some(Point {
                    x: num(tk.get(j + 1))?,
                    y: num(tk.get(j + 2))?,
                });
                j += 2;
            }
            "size" => {
                p.size = Some(Point {
                    x: num(tk.get(j + 1))?,
                    y: num(tk.get(j + 2))?,
                });
                j += 2;
            }
            "offset" => {
                p.offset = Some(Point {
                    x: num(tk.get(j + 1))?,
                    y: num(tk.get(j + 2))?,
                });
                j += 2;
            }
            "states" => {
                let v: Vec<String> = word(tk.get(j + 1), "states a|b|c")?
                    .split('|')
                    .filter(|s| !s.is_empty())
                    .map(str::to_string)
                    .collect();
                if v.is_empty() {
                    return Err("states needs at least one name".into());
                }
                states = Some(v);
                j += 1;
            }
            "values" => {
                p.values = word(tk.get(j + 1), "values 10|50|90")?
                    .split('|')
                    .map(|v| num(Some(&Tok::Word(v.to_string()))))
                    .collect::<Result<_, _>>()?;
                j += 1;
            }
            "state" => {
                init = Some(word(tk.get(j + 1), "a state name")?.to_string());
                j += 1;
            }
            "back" => match tk.get(j + 1) {
                Some(Tok::Text(b)) => {
                    p.back = Some(b.clone());
                    j += 1;
                }
                _ => return Err("back needs \"quoted text\"".into()),
            },
            "on" => {
                let mut words = Vec::new();
                while let Some(w) = tk
                    .get(j + 1)
                    .and_then(Tok::word)
                    .filter(|w| !KEYS.contains(w))
                {
                    words.push(w);
                    j += 1;
                }
                p.route = id_list(words.into_iter());
            }
            other => return Err(format!("unexpected \"{other}\"")),
        }
        j += 1;
    }
    match kind {
        Kind::Token if p.route.is_empty() => return Err("token needs: on a, b, c".into()),
        Kind::Bar if p.values.is_empty() => return Err("bar needs: values 10|50|90".into()),
        Kind::Token => p.states = (0..p.route.len()).map(|k| k.to_string()).collect(),
        Kind::Bar => p.states = p.values.iter().map(ToString::to_string).collect(),
        Kind::Card | Kind::Note => {}
        _ => {
            if let Some(s) = states {
                p.states = s;
            }
        }
    }
    if let Some(l) = label {
        p.label = l;
    }
    Ok(Pending {
        piece: p,
        line: 0,
        init,
    })
}

fn quote(s: &str) -> String {
    let mut out = String::with_capacity(s.len() + 2);
    out.push('"');
    for c in s.chars() {
        match c {
            '"' => out.push_str("\\\""),
            '\\' => out.push_str("\\\\"),
            '\n' => out.push_str("\\n"),
            c => out.push(c),
        }
    }
    out.push('"');
    out
}

/// Canonical DSL for a board. `parse(&format(b)).board == *b`.
pub fn format(board: &Board) -> String {
    let mut out = String::new();
    for p in &board.pieces {
        let _ = write!(out, "{} {}", p.kind.name(), p.id);
        if let (Some(f), Some(t)) = (&p.from, &p.to) {
            let _ = write!(out, " {f} -> {t}");
        }
        let default_label = if p.kind == Kind::Arrow {
            ""
        } else {
            p.id.as_str()
        };
        if p.label != default_label {
            let _ = write!(out, " {}", quote(&p.label));
        }
        if !p.route.is_empty() {
            let _ = write!(out, " on {}", p.route.join(", "));
        }
        if let Some(b) = &p.back {
            let _ = write!(out, " back {}", quote(b));
        }
        if let Some(a) = p.at {
            let _ = write!(out, " at {} {}", a.x, a.y);
        }
        if let Some(s) = p.size {
            let _ = write!(out, " size {} {}", s.x, s.y);
        }
        if let Some(o) = p.offset {
            let _ = write!(out, " offset {} {}", o.x, o.y);
        }
        if !p.values.is_empty() {
            let v: Vec<_> = p.values.iter().map(ToString::to_string).collect();
            let _ = write!(out, " values {}", v.join("|"));
        }
        let defaults: Vec<String> = p
            .kind
            .default_states()
            .iter()
            .map(ToString::to_string)
            .collect();
        let derived = matches!(p.kind, Kind::Token | Kind::Bar | Kind::Card | Kind::Note);
        if !derived && p.states != defaults {
            let _ = write!(out, " states {}", p.states.join("|"));
        }
        if p.base != 0 {
            let _ = write!(out, " state {}", p.states[p.base]);
        }
        out.push('\n');
    }
    for l in &board.links {
        let _ = writeln!(out, "link {} -> {}", l.from, l.to.join(", "));
    }
    for s in &board.steps {
        out.push_str("step");
        if !s.caption.is_empty() {
            let _ = write!(out, " {}", quote(&s.caption));
        }
        for a in &s.set {
            let name = board
                .piece(&a.piece)
                .map_or_else(|| a.state.to_string(), |p| p.states[a.state].clone());
            let _ = write!(out, " {}={name}", a.piece);
        }
        out.push('\n');
    }
    if let Some(s) = board.sim {
        let _ = writeln!(
            out,
            "sim {} {} scene {} seed {}",
            s.width,
            s.height,
            s.scene.name(),
            s.seed
        );
    }
    out
}

/// Instructions for any LLM: the whole vocabulary, nothing else.
pub const AGENT_PROMPT: &str = r#"You are drawing on Blockies, a whiteboard where every element is a small game piece with a FIXED set of states. People drag pieces between their declared states and the board animates everything in between. You may only use the 8 pieces and 3 statements below. Anything that changes must be expressed as states.

Reply with one code block of lines (no other syntax). Coordinates: x 0-1400, y 0-900; positions are piece centres. Keep ~140px gaps between boxes; put controls (switch/slider/bar) in a row below the diagram.

box <id> "Label" at X Y [size W H] [states a|b|c] [state a]
arrow <id> <fromId> -> <toId> [states off|on|busy] ["label"]
token <id> "Label" on <id>, <id>, <id> [offset DX DY]   # travels a route over boxes; its state is the route index (0-based)
switch <id> "Label" at X Y [states off|on] [state on]
slider <id> "Label" at X Y states a|b|c
bar <id> "Label" at X Y values 10|50|90                  # 0-100; one value per state
card <id> "Front text" back "Back text" at X Y           # flips front|back
note <id> "Text, use \n for new lines" at X Y
link <sourceId> -> <targetId>, <targetId>                  # targets follow the source's state proportionally
step "Caption" <id>=<state> <id>=<state>                   # cumulative steps, played like slides; token/bar states are numbers
sim <W> <H> [scene empty|calm|storm] [seed N]              # optional block field (falling sand/water), W and H multiples of 64

Rules:
- ids: unique, lowercase, no spaces. Labels in "double quotes".
- Box state names colour themselves: ok/done/on/ready (green), busy/waiting/doing/review (amber), down/error/blocked/overloaded/spike (red), idle/off/todo/quiet (neutral). Other names get accent colours.
- One token per thing that moves. A route may revisit boxes (e.g. a request and its response).
- Never set a linked target inside a step; steps control only independent pieces.
- Prefer 3-8 boxes, 0-3 controls, 3-7 steps that tell the story in order.

Draw: "#;

/// Example boards, the same stories as the original prototype.
pub const EXAMPLES: &[(&str, &str)] = &[
    ("web", include_str!("../examples/web.blk")),
    ("water", include_str!("../examples/water.blk")),
    ("sprint", include_str!("../examples/sprint.blk")),
    ("sandbox", include_str!("../examples/sandbox.blk")),
];

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn examples_parse_cleanly_and_round_trip() {
        for (name, src) in EXAMPLES {
            let p = parse(src);
            assert!(p.errors.is_empty(), "{name}: {:?}", p.errors);
            assert!(!p.board.pieces.is_empty());
            let again = parse(&format(&p.board));
            assert!(
                again.errors.is_empty(),
                "{name} reformatted: {:?}",
                again.errors
            );
            assert_eq!(again.board, p.board, "{name} round trip");
        }
    }

    #[test]
    fn bad_lines_are_reported_and_good_lines_kept() {
        let src = "box a \"A\" at 0 0 states idle|busy\nblob x\nbox b at 10 10\narrow e a -> nope\nstep \"go\" a=busy b=gone\nbox a at 1 1";
        let p = parse(src);
        let ids: Vec<_> = p.board.pieces.iter().map(|p| p.id.as_str()).collect();
        assert_eq!(ids, ["a", "b"]);
        let lines: Vec<_> = p.errors.iter().map(|e| e.line).collect();
        assert_eq!(lines, [2, 4, 5, 6], "{:?}", p.errors);
        assert_eq!(
            p.board.steps[0].set,
            [Assign {
                piece: "a".into(),
                state: 1
            }]
        );
    }

    #[test]
    fn tokens_bars_and_links() {
        let p = parse(
            "box x at 0 0\nbox y at 100 0\ntoken t \"T\" on x, y, x\nslider s \"S\" at 0 100 states a|b|c\nbar v \"V\" at 0 200 values 5|50|95\nlink s -> v\nstep v=1",
        );
        assert_eq!(p.board.piece("t").unwrap().states, ["0", "1", "2"]);
        assert_eq!(p.board.piece("v").unwrap().states, ["5", "50", "95"]);
        assert_eq!(p.board.links.len(), 1);
        assert_eq!(p.errors.len(), 1, "linked target in a step is refused");
    }

    #[test]
    fn sim_line() {
        let p = parse("sim 256 128 scene calm seed 9");
        assert_eq!(
            p.board.sim,
            Some(SimSpec {
                width: 256,
                height: 128,
                scene: Scene::Calm,
                seed: 9
            })
        );
        assert_eq!(parse("sim 100 128").errors.len(), 1);
        assert_eq!(parse("sim 128 128\nsim 128 128").errors.len(), 1);
    }

    #[test]
    fn quotes_and_escapes() {
        let p = parse(r#"note n "Two\nlines with \"quotes\"" at 1 2"#);
        assert_eq!(p.board.pieces[0].label, "Two\nlines with \"quotes\"");
        assert_eq!(parse(&format(&p.board)).board, p.board);
        assert_eq!(
            parse("note n \"open").errors[0].message,
            "unclosed \"quote\""
        );
    }
}
