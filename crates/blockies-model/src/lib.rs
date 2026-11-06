//! The Blockies vocabulary: eight pieces with declared states, links, steps and an optional
//! block field. Pure data, no I/O, so it compiles everywhere (server, TUI, WASM).
//!
//! Few choices, on purpose: agents get exactly these pieces and statements.

use serde::{Deserialize, Serialize};

/// The eight pieces. New kinds must justify themselves (see AGENTS.md).
#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum Kind {
    Box,
    Arrow,
    Token,
    Switch,
    Slider,
    Bar,
    Card,
    Note,
}

impl Kind {
    pub const ALL: [Self; 8] = [
        Self::Box,
        Self::Arrow,
        Self::Token,
        Self::Switch,
        Self::Slider,
        Self::Bar,
        Self::Card,
        Self::Note,
    ];

    pub const fn name(self) -> &'static str {
        match self {
            Self::Box => "box",
            Self::Arrow => "arrow",
            Self::Token => "token",
            Self::Switch => "switch",
            Self::Slider => "slider",
            Self::Bar => "bar",
            Self::Card => "card",
            Self::Note => "note",
        }
    }

    pub fn parse(s: &str) -> Option<Self> {
        Self::ALL.into_iter().find(|k| k.name() == s)
    }

    /// States used when the DSL line declares none.
    pub const fn default_states(self) -> &'static [&'static str] {
        match self {
            Self::Box => &["idle"],
            Self::Arrow | Self::Switch => &["off", "on"],
            Self::Slider => &["low", "mid", "high"],
            Self::Card => &["front", "back"],
            Self::Note => &["note"],
            // Token and bar states are derived from their route and values.
            Self::Token | Self::Bar => &[],
        }
    }

    /// Pieces that are placed with `at X Y` (arrows and tokens are positioned by others).
    pub const fn is_placed(self) -> bool {
        !matches!(self, Self::Arrow | Self::Token)
    }
}

/// Board coordinates. Positions are piece centres; boards are drawn on roughly 1400 × 900.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct Point {
    pub x: i32,
    pub y: i32,
}

#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize)]
pub struct Piece {
    pub id: String,
    pub kind: Kind,
    pub label: String,
    /// Declared states, in order. A piece is always in exactly one of them.
    pub states: Vec<String>,
    /// Initial state index (`state X` in the DSL).
    pub base: usize,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub at: Option<Point>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub size: Option<Point>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub offset: Option<Point>,
    /// Arrow endpoints.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub from: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub to: Option<String>,
    /// Token route (piece ids); state `k` means "at route[k]".
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub route: Vec<String>,
    /// Bar values (0..=100), one per state.
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub values: Vec<i32>,
    /// Card back text.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub back: Option<String>,
}

impl Piece {
    pub fn new(id: impl Into<String>, kind: Kind) -> Self {
        let id = id.into();
        Self {
            label: if kind == Kind::Arrow {
                String::new()
            } else {
                id.clone()
            },
            id,
            kind,
            states: kind
                .default_states()
                .iter()
                .map(ToString::to_string)
                .collect(),
            base: 0,
            at: None,
            size: None,
            offset: None,
            from: None,
            to: None,
            route: Vec::new(),
            values: Vec::new(),
            back: None,
        }
    }

    /// Resolves a state by name, or by index when the name is a number.
    pub fn resolve_state(&self, v: &str) -> Option<usize> {
        if let Some(i) = self.states.iter().position(|s| s == v) {
            return Some(i);
        }
        v.parse::<usize>().ok().filter(|&i| i < self.states.len())
    }

    pub fn last_state(&self) -> usize {
        self.states.len().saturating_sub(1)
    }
}

/// `link src -> a, b`: targets follow the source's state proportionally.
#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize)]
pub struct Link {
    pub from: String,
    pub to: Vec<String>,
}

/// One assignment inside a step.
#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize)]
pub struct Assign {
    pub piece: String,
    pub state: usize,
}

/// `step "Caption" id=state ...`: steps are cumulative and played like slides.
#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize)]
pub struct Step {
    pub caption: String,
    pub set: Vec<Assign>,
}

/// Starting content of a block field.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, Hash, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum Scene {
    /// Platforms only.
    #[default]
    Empty,
    /// A settled pile with a few falling streams.
    Calm,
    /// Sand and water everywhere, emitters on every chunk.
    Storm,
}

impl Scene {
    pub const ALL: [Self; 3] = [Self::Empty, Self::Calm, Self::Storm];

    pub const fn name(self) -> &'static str {
        match self {
            Self::Empty => "empty",
            Self::Calm => "calm",
            Self::Storm => "storm",
        }
    }

    pub fn parse(s: &str) -> Option<Self> {
        Self::ALL.into_iter().find(|k| k.name() == s)
    }
}

/// `sim W H [scene S] [seed N]`: the board's `[c]` block field, run in lockstep everywhere.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize, Deserialize)]
pub struct SimSpec {
    pub width: u32,
    pub height: u32,
    pub scene: Scene,
    pub seed: u32,
}

/// Block materials. One byte per cell; the numbering is part of the determinism contract.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
#[repr(u8)]
pub enum Material {
    Empty = 0,
    Sand = 1,
    Water = 2,
    Wall = 3,
}

impl Material {
    pub const ALL: [Self; 4] = [Self::Empty, Self::Sand, Self::Water, Self::Wall];

    pub const fn name(self) -> &'static str {
        match self {
            Self::Empty => "empty",
            Self::Sand => "sand",
            Self::Water => "water",
            Self::Wall => "wall",
        }
    }

    pub fn parse(s: &str) -> Option<Self> {
        Self::ALL.into_iter().find(|k| k.name() == s)
    }
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct Board {
    pub pieces: Vec<Piece>,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub links: Vec<Link>,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub steps: Vec<Step>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub sim: Option<SimSpec>,
}

impl Board {
    pub fn index(&self, id: &str) -> Option<usize> {
        self.pieces.iter().position(|p| p.id == id)
    }

    pub fn piece(&self, id: &str) -> Option<&Piece> {
        self.pieces.iter().find(|p| p.id == id)
    }

    /// Pieces that are link targets. They follow their source and cannot be set directly.
    pub fn driven(&self) -> Vec<bool> {
        let mut d = vec![false; self.pieces.len()];
        for l in &self.links {
            for t in &l.to {
                if let Some(i) = self.index(t) {
                    d[i] = true;
                }
            }
        }
        d
    }

    /// Initial state of every piece.
    pub fn base_states(&self) -> Vec<usize> {
        self.pieces.iter().map(|p| p.base).collect()
    }
}

/// Semantic colour of a state, decided by its name (so agents get colour for free).
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Tone {
    Good,
    Busy,
    Bad,
    Neutral,
    /// Any other name: one of four accent colours, by state index.
    Accent(u8),
}

pub fn tone(state: &str, index: usize) -> Tone {
    const GOOD: &[&str] = &[
        "ok", "up", "done", "on", "healthy", "active", "warm", "ready", "live", "pass", "hit",
        "open", "success", "green", "full", "shipped", "flowing",
    ];
    const BUSY: &[&str] = &[
        "busy", "waiting", "slow", "doing", "pending", "warning", "warn", "review", "loading",
        "normal", "partial", "medium", "heavy",
    ];
    const BAD: &[&str] = &[
        "down",
        "error",
        "fail",
        "failed",
        "overloaded",
        "blocked",
        "dead",
        "hot",
        "spike",
        "miss",
        "closed",
        "critical",
        "high",
        "storm",
        "flood",
    ];
    const NEUTRAL: &[&str] = &[
        "idle", "off", "todo", "none", "empty", "quiet", "new", "inactive", "cold", "low", "front",
        "note", "calm", "dry", "thin",
    ];
    let s = state.to_ascii_lowercase();
    let s = s.as_str();
    if GOOD.contains(&s) {
        Tone::Good
    } else if BUSY.contains(&s) {
        Tone::Busy
    } else if BAD.contains(&s) {
        Tone::Bad
    } else if NEUTRAL.contains(&s) {
        Tone::Neutral
    } else {
        Tone::Accent((index % 4) as u8)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn resolve_by_name_or_index() {
        let mut p = Piece::new("api", Kind::Box);
        p.states = vec!["idle".into(), "busy".into()];
        assert_eq!(p.resolve_state("busy"), Some(1));
        assert_eq!(p.resolve_state("0"), Some(0));
        assert_eq!(p.resolve_state("2"), None);
        assert_eq!(p.resolve_state("gone"), None);
    }

    #[test]
    fn tones() {
        assert_eq!(tone("Done", 0), Tone::Good);
        assert_eq!(tone("overloaded", 2), Tone::Bad);
        assert_eq!(tone("purple", 5), Tone::Accent(1));
    }
}
