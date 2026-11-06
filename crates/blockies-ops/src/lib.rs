//! One command layer for every surface. The server, the CLI, the TUI and (later) MCP all turn
//! user or agent input into an [`Op`], and [`Doc::apply`] is the only code that validates and
//! changes a board. Errors carry a stable `code` so agents can self-correct.

use blockies_dsl::LineError;
use blockies_model::{Board, Kind, Material};
use serde::{Deserialize, Serialize};

/// Everything a client may ask a board to do.
#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize)]
#[serde(tag = "op", rename_all = "snake_case")]
pub enum Op {
    /// Put a piece in a state (by name, or by index for tokens and bars).
    Set { piece: String, state: String },
    /// Move a token to a piece on its route (the next visit after where it is now).
    Move { piece: String, to: String },
    /// Advance a piece to its next state, wrapping around.
    Cycle { piece: String },
    /// Walk the story: `next`, `prev`, `start`, or a 1-based step number.
    Step { to: String },
    /// Replace the board with new DSL. Valid lines load; problems come back as warnings.
    Load { dsl: String },
    /// Paint a disc of material into the block field (applied at the next tick, in order).
    Paint {
        x: i32,
        y: i32,
        r: u16,
        material: Material,
    },
}

/// A change every client applies to stay in sync.
#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize)]
#[serde(tag = "kind", rename_all = "snake_case")]
pub enum Patch {
    State {
        piece: String,
        state: usize,
    },
    Step {
        step: Option<usize>,
    },
    /// The board was replaced; clients re-parse `dsl` and take `states`.
    Board {
        dsl: String,
        states: Vec<usize>,
        step: Option<usize>,
    },
}

/// A block-field intent, ordered into a tick by the board owner.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize, Deserialize)]
pub struct Paint {
    pub x: i32,
    pub y: i32,
    pub r: u16,
    pub material: Material,
}

#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct Applied {
    pub patches: Vec<Patch>,
    /// Set when the op targets the block field; the caller schedules it into a tick.
    pub paint: Option<Paint>,
    /// DSL problems from `Load` (the valid lines were applied).
    pub warnings: Vec<LineError>,
}

/// A refused op. `code` is stable and machine-readable; `detail` says how to fix it.
#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize)]
pub struct OpError {
    pub code: &'static str,
    pub detail: String,
}

impl std::fmt::Display for OpError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "{}: {}", self.code, self.detail)
    }
}

impl std::error::Error for OpError {}

fn err(code: &'static str, detail: impl Into<String>) -> OpError {
    OpError {
        code,
        detail: detail.into(),
    }
}

/// A board plus where every piece currently is.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Doc {
    board: Board,
    states: Vec<usize>,
    step: Option<usize>,
    rev: u64,
    driven: Vec<bool>,
}

impl Doc {
    pub fn new(board: Board) -> Self {
        let states = board.base_states();
        let driven = board.driven();
        let mut doc = Self {
            board,
            states,
            step: None,
            rev: 0,
            driven,
        };
        doc.propagate();
        doc
    }

    /// Parses DSL; valid lines load, problems are returned alongside.
    pub fn from_dsl(text: &str) -> (Self, Vec<LineError>) {
        let p = blockies_dsl::parse(text);
        (Self::new(p.board), p.errors)
    }

    /// Rebuilds a doc a server sent (`Welcome` / `Patch::Board`).
    pub fn restore(dsl: &str, states: &[usize], step: Option<usize>, rev: u64) -> Self {
        let mut doc = Self::from_dsl(dsl).0;
        for (s, &v) in doc.states.iter_mut().zip(states) {
            *s = v;
        }
        doc.clamp_states();
        doc.step = step.filter(|&k| k < doc.board.steps.len());
        doc.rev = rev;
        doc
    }

    pub const fn board(&self) -> &Board {
        &self.board
    }

    pub fn states(&self) -> &[usize] {
        &self.states
    }

    pub const fn step(&self) -> Option<usize> {
        self.step
    }

    pub const fn rev(&self) -> u64 {
        self.rev
    }

    pub fn is_driven(&self, i: usize) -> bool {
        self.driven.get(i).copied().unwrap_or(false)
    }

    pub fn dsl(&self) -> String {
        blockies_dsl::format(&self.board)
    }

    /// State name of a piece.
    pub fn state_name(&self, i: usize) -> &str {
        &self.board.pieces[i].states[self.states[i]]
    }

    /// Caption of the current step ("" before the first step).
    pub fn caption(&self) -> &str {
        self.step
            .map_or("", |k| self.board.steps[k].caption.as_str())
    }

    fn find(&self, id: &str) -> Result<usize, OpError> {
        self.board.index(id).ok_or_else(|| {
            let ids: Vec<_> = self.board.pieces.iter().map(|p| p.id.as_str()).collect();
            err(
                "unknown_piece",
                format!("no piece \"{id}\" (pieces: {})", ids.join(", ")),
            )
        })
    }

    fn settable(&self, id: &str) -> Result<usize, OpError> {
        let i = self.find(id)?;
        if self.driven[i] {
            let src = self
                .board
                .links
                .iter()
                .find(|l| l.to.iter().any(|t| t == id))
                .map_or("", |l| l.from.as_str());
            return Err(err(
                "driven",
                format!("\"{id}\" follows \"{src}\" through a link; set \"{src}\" instead"),
            ));
        }
        Ok(i)
    }

    /// Validates and applies one op. On error nothing changes.
    pub fn apply(&mut self, op: &Op) -> Result<Applied, OpError> {
        let before = self.states.clone();
        let before_step = self.step;
        let mut out = Applied::default();
        match op {
            Op::Set { piece, state } => {
                let i = self.settable(piece)?;
                let p = &self.board.pieces[i];
                let k = p.resolve_state(state).ok_or_else(|| {
                    err(
                        "unknown_state",
                        format!(
                            "\"{piece}\" has no state \"{state}\" (has {})",
                            p.states.join("|")
                        ),
                    )
                })?;
                self.states[i] = k;
            }
            Op::Cycle { piece } => {
                let i = self.settable(piece)?;
                self.states[i] = (self.states[i] + 1) % self.board.pieces[i].states.len().max(1);
            }
            Op::Move { piece, to } => {
                let i = self.settable(piece)?;
                let p = &self.board.pieces[i];
                if p.kind != Kind::Token {
                    return self.apply(&Op::Set {
                        piece: piece.clone(),
                        state: to.clone(),
                    });
                }
                let n = p.route.len();
                let cur = self.states[i];
                // The next visit of `to` after the current stop, wrapping around the route.
                let k = (1..=n)
                    .map(|d| (cur + d) % n)
                    .find(|&k| p.route[k] == *to)
                    .ok_or_else(|| {
                        err(
                            "not_on_route",
                            format!(
                                "\"{to}\" is not on {piece}'s route ({})",
                                p.route.join(", ")
                            ),
                        )
                    })?;
                self.states[i] = k;
            }
            Op::Step { to } => {
                let n = self.board.steps.len();
                if n == 0 {
                    return Err(err(
                        "no_steps",
                        "this board has no steps; add `step \"Caption\" id=state` lines",
                    ));
                }
                let target = match to.as_str() {
                    "start" => None,
                    "next" => Some(self.step.map_or(0, |k| (k + 1).min(n - 1))),
                    "prev" => self.step.and_then(|k| k.checked_sub(1)),
                    s => match s.parse::<usize>() {
                        Ok(k) if (1..=n).contains(&k) => Some(k - 1),
                        _ => {
                            return Err(err(
                                "bad_step",
                                format!("use next, prev, start or 1..{n}, got \"{s}\""),
                            ));
                        }
                    },
                };
                self.go_step(target);
            }
            Op::Load { dsl } => {
                let p = blockies_dsl::parse(dsl);
                if p.board.pieces.is_empty() && !p.errors.is_empty() {
                    return Err(err(
                        "bad_board",
                        format!("nothing loaded: {}", p.errors[0].message),
                    ));
                }
                self.replace(p.board);
                out.warnings = p.errors;
                out.patches.push(Patch::Board {
                    dsl: self.dsl(),
                    states: self.states.clone(),
                    step: self.step,
                });
                self.rev += 1;
                return Ok(out);
            }
            Op::Paint { x, y, r, material } => {
                if self.board.sim.is_none() {
                    return Err(err(
                        "no_sim",
                        "this board has no block field; add `sim 512 256` to its DSL",
                    ));
                }
                out.paint = Some(Paint {
                    x: *x,
                    y: *y,
                    r: *r,
                    material: *material,
                });
                self.rev += 1;
                return Ok(out);
            }
        }
        self.propagate();
        for (i, (&a, &b)) in before.iter().zip(&self.states).enumerate() {
            if a != b {
                out.patches.push(Patch::State {
                    piece: self.board.pieces[i].id.clone(),
                    state: b,
                });
            }
        }
        if self.step != before_step {
            out.patches.push(Patch::Step { step: self.step });
        }
        self.rev += 1;
        Ok(out)
    }

    /// Applies a patch received from the board owner (clients only; no validation needed).
    pub fn apply_patch(&mut self, patch: &Patch, rev: u64) {
        match patch {
            Patch::State { piece, state } => {
                if let Some(i) = self.board.index(piece) {
                    self.states[i] = (*state).min(self.board.pieces[i].last_state());
                }
            }
            Patch::Step { step } => self.step = step.filter(|&k| k < self.board.steps.len()),
            Patch::Board { dsl, states, step } => *self = Self::restore(dsl, states, *step, rev),
        }
        self.rev = rev;
    }

    fn go_step(&mut self, target: Option<usize>) {
        let mut t = self.board.base_states();
        if let Some(k) = target {
            for s in &self.board.steps[..=k] {
                for a in &s.set {
                    if let Some(i) = self.board.index(&a.piece) {
                        t[i] = a.state;
                    }
                }
            }
        }
        for (i, v) in t.into_iter().enumerate() {
            if !self.driven[i] {
                self.states[i] = v;
            }
        }
        self.step = target;
    }

    /// Link targets follow their source proportionally (chains settle in link order).
    fn propagate(&mut self) {
        for _ in 0..=self.board.links.len() {
            let mut moved = false;
            for l in &self.board.links {
                let Some(s) = self.board.index(&l.from) else {
                    continue;
                };
                let ns = self.board.pieces[s].last_state();
                for id in &l.to {
                    let Some(d) = self.board.index(id) else {
                        continue;
                    };
                    let nd = self.board.pieces[d].last_state();
                    let v = if ns == 0 {
                        0
                    } else {
                        (self.states[s] * nd * 2 + ns) / (ns * 2)
                    };
                    if self.states[d] != v {
                        self.states[d] = v;
                        moved = true;
                    }
                }
            }
            if !moved {
                break;
            }
        }
    }

    /// New board, keeping the state of pieces that survive (same id and kind).
    fn replace(&mut self, board: Board) {
        let old: Vec<_> = self
            .board
            .pieces
            .iter()
            .zip(&self.states)
            .map(|(p, &s)| (p.id.clone(), p.kind, s))
            .collect();
        self.states = board.base_states();
        for (i, p) in board.pieces.iter().enumerate() {
            if let Some((_, _, s)) = old.iter().find(|(id, k, _)| *id == p.id && *k == p.kind) {
                self.states[i] = (*s).min(p.last_state());
            }
        }
        self.driven = board.driven();
        self.board = board;
        self.step = self.step.filter(|&k| k < self.board.steps.len());
        self.propagate();
    }

    fn clamp_states(&mut self) {
        for (s, p) in self.states.iter_mut().zip(&self.board.pieces) {
            *s = (*s).min(p.last_state());
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    const WEB: &str = include_str!("../../blockies-dsl/examples/web.blk");

    fn set(piece: &str, state: &str) -> Op {
        Op::Set {
            piece: piece.into(),
            state: state.into(),
        }
    }

    #[test]
    fn set_and_link_follow() {
        let (mut d, errs) = Doc::from_dsl(WEB);
        assert!(errs.is_empty());
        let a = d.apply(&set("load", "spike")).unwrap();
        let api = d.board().index("api").unwrap();
        let cpu = d.board().index("cpu").unwrap();
        assert_eq!(d.state_name(api), "overloaded");
        assert_eq!(d.states()[cpu], 2);
        assert_eq!(a.patches.len(), 3, "{:?}", a.patches);
        assert_eq!(d.rev(), 1);
    }

    #[test]
    fn errors_change_nothing_and_say_why() {
        let (mut d, _) = Doc::from_dsl(WEB);
        let before = d.clone();
        assert_eq!(d.apply(&set("api", "busy")).unwrap_err().code, "driven");
        assert_eq!(
            d.apply(&set("nope", "x")).unwrap_err().code,
            "unknown_piece"
        );
        assert_eq!(
            d.apply(&set("browser", "melted")).unwrap_err().code,
            "unknown_state"
        );
        assert_eq!(
            d.apply(&Op::Step { to: "9".into() }).unwrap_err().code,
            "bad_step"
        );
        assert_eq!(
            d.apply(&Op::Paint {
                x: 0,
                y: 0,
                r: 1,
                material: Material::Sand
            })
            .unwrap_err()
            .code,
            "no_sim"
        );
        assert_eq!(d, before);
    }

    #[test]
    fn steps_are_cumulative() {
        let (mut d, _) = Doc::from_dsl(WEB);
        let req = d.board().index("req").unwrap();
        d.apply(&Op::Step { to: "4".into() }).unwrap();
        assert_eq!(d.states()[req], 3);
        assert_eq!(d.caption(), "API asks the database");
        d.apply(&Op::Step { to: "next".into() }).unwrap();
        assert_eq!(d.states()[req], 6);
        d.apply(&Op::Step { to: "start".into() }).unwrap();
        assert_eq!(d.states()[req], 0);
        assert_eq!(d.step(), None);
    }

    #[test]
    fn move_token_to_next_visit() {
        let (mut d, _) = Doc::from_dsl(WEB);
        let req = d.board().index("req").unwrap();
        let mv = |to: &str| Op::Move {
            piece: "req".into(),
            to: to.into(),
        };
        d.apply(&mv("api")).unwrap();
        assert_eq!(d.states()[req], 2);
        d.apply(&mv("api")).unwrap();
        assert_eq!(d.states()[req], 4, "second visit on the way back");
        assert_eq!(d.apply(&mv("cache")).unwrap_err().code, "not_on_route");
    }

    #[test]
    fn load_keeps_surviving_states() {
        let (mut d, _) = Doc::from_dsl("box a states x|y|z\nbox b states p|q");
        d.apply(&set("a", "z")).unwrap();
        let r = d
            .apply(&Op::Load {
                dsl: "box a states x|y|z\nbox c\nbogus".into(),
            })
            .unwrap();
        assert_eq!(d.state_name(0), "z");
        assert_eq!(r.warnings.len(), 1);
        assert!(matches!(r.patches[0], Patch::Board { .. }));
    }

    #[test]
    fn patches_replay_to_the_same_doc() {
        let (mut owner, _) = Doc::from_dsl(WEB);
        let mut client = Doc::restore(&owner.dsl(), owner.states(), owner.step(), owner.rev());
        for op in [
            set("load", "normal"),
            Op::Step { to: "3".into() },
            set("cache", "on"),
            Op::Cycle { piece: "db".into() },
        ] {
            let a = owner.apply(&op).unwrap();
            for p in &a.patches {
                client.apply_patch(p, owner.rev());
            }
        }
        assert_eq!(client.states(), owner.states());
        assert_eq!(client.step(), owner.step());
    }

    #[test]
    fn ops_are_plain_json() {
        let op: Op = serde_json::from_str(r#"{"op":"move","piece":"req","to":"db"}"#).unwrap();
        assert_eq!(
            op,
            Op::Move {
                piece: "req".into(),
                to: "db".into()
            }
        );
        let paint: Op =
            serde_json::from_str(r#"{"op":"paint","x":1,"y":2,"r":3,"material":"water"}"#).unwrap();
        assert!(matches!(
            paint,
            Op::Paint {
                material: Material::Water,
                ..
            }
        ));
    }
}
