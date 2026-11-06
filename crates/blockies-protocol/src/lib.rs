//! Protocol v1. JSON text frames, one message per frame, tagged by `"t"`.
//!
//! Two layers travel on one socket:
//! - **Semantic**: ops in, patches out (small, validated by the board owner).
//! - **Block field**: lockstep. The owner orders paints into ticks and broadcasts
//!   `Tick { tick, paints }`; every client runs the same deterministic sim and reports
//!   `Hash` now and then. A mismatch (or a join) is answered with a compressed `Snapshot`.

use base64::Engine as _;
use blockies_dsl::LineError;
use blockies_model::SimSpec;
use blockies_ops::{Op, Paint, Patch};
use blockies_sim::World;
use serde::{Deserialize, Serialize};

pub const PROTOCOL: u32 = 1;

/// Clients send their sim hash every this many ticks.
pub const HASH_EVERY: u32 = 30;

#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize)]
#[serde(tag = "t", rename_all = "snake_case")]
pub enum ClientMsg {
    /// `id` is chosen by the client and echoed in `Ack` / `Reject`.
    Op { id: u64, op: Op },
    /// Sim hash after `tick` ticks (hex, since JS numbers cannot hold a u64).
    Hash { tick: u32, hash: String },
    /// Ask for a fresh sim snapshot.
    Resync,
}

#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize)]
#[serde(tag = "t", rename_all = "snake_case")]
pub enum ServerMsg {
    Welcome {
        protocol: u32,
        board: String,
        you: u32,
        rev: u64,
        dsl: String,
        states: Vec<usize>,
        step: Option<usize>,
        #[serde(default, skip_serializing_if = "Option::is_none")]
        sim: Option<Snapshot>,
    },
    Ack {
        id: u64,
        rev: u64,
        #[serde(default, skip_serializing_if = "Vec::is_empty")]
        warnings: Vec<LineError>,
    },
    Reject {
        id: u64,
        code: String,
        detail: String,
    },
    Patch {
        rev: u64,
        by: u32,
        patches: Vec<Patch>,
    },
    /// Apply `paints` in order, then step once. Before: `world.tick() == tick`.
    Tick {
        tick: u32,
        #[serde(default, skip_serializing_if = "Vec::is_empty")]
        paints: Vec<Paint>,
    },
    Snapshot {
        sim: Snapshot,
    },
}

/// A block field at a tick: LZ4-compressed cells, base64 for JSON.
#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize)]
pub struct Snapshot {
    pub spec: SimSpec,
    pub tick: u32,
    pub cells: String,
}

#[derive(Debug)]
pub enum SnapshotError {
    Base64(base64::DecodeError),
    Lz4(lz4_flex::block::DecompressError),
    Sim(blockies_sim::SimError),
}

impl std::fmt::Display for SnapshotError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::Base64(e) => write!(f, "snapshot base64: {e}"),
            Self::Lz4(e) => write!(f, "snapshot lz4: {e}"),
            Self::Sim(e) => write!(f, "snapshot: {e}"),
        }
    }
}

impl std::error::Error for SnapshotError {}

impl Snapshot {
    pub fn of(world: &World) -> Self {
        let packed = lz4_flex::compress_prepend_size(world.cells());
        Self {
            spec: world.spec(),
            tick: world.tick(),
            cells: base64::engine::general_purpose::STANDARD.encode(packed),
        }
    }

    pub fn world(&self) -> Result<World, SnapshotError> {
        let packed = base64::engine::general_purpose::STANDARD
            .decode(&self.cells)
            .map_err(SnapshotError::Base64)?;
        let cells = lz4_flex::decompress_size_prepended(&packed).map_err(SnapshotError::Lz4)?;
        World::restore(self.spec, self.tick, cells).map_err(SnapshotError::Sim)
    }
}

/// HTTP view of a board (`GET /api/boards/{name}`), shaped for agents: names, not indices.
#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize)]
pub struct BoardView {
    pub name: String,
    pub rev: u64,
    /// 1-based current step (0 = before the first step) and the number of steps.
    pub step: usize,
    pub steps: usize,
    pub caption: String,
    pub pieces: Vec<PieceView>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub sim: Option<SimView>,
    pub dsl: String,
}

#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize)]
pub struct PieceView {
    pub id: String,
    pub kind: blockies_model::Kind,
    pub label: String,
    pub state: String,
    pub states: Vec<String>,
    /// Follows a link; set its source instead.
    #[serde(default, skip_serializing_if = "std::ops::Not::not")]
    pub driven: bool,
}

#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize)]
pub struct SimView {
    pub spec: SimSpec,
    pub tick: u32,
    pub hash: String,
}

impl BoardView {
    pub fn new(name: &str, doc: &blockies_ops::Doc, world: Option<&World>) -> Self {
        let b = doc.board();
        Self {
            name: name.to_string(),
            rev: doc.rev(),
            step: doc.step().map_or(0, |k| k + 1),
            steps: b.steps.len(),
            caption: doc.caption().to_string(),
            pieces: b
                .pieces
                .iter()
                .enumerate()
                .map(|(i, p)| PieceView {
                    id: p.id.clone(),
                    kind: p.kind,
                    label: p.label.clone(),
                    state: doc.state_name(i).to_string(),
                    states: p.states.clone(),
                    driven: doc.is_driven(i),
                })
                .collect(),
            sim: world.map(|w| SimView {
                spec: w.spec(),
                tick: w.tick(),
                hash: hash_hex(w.hash()),
            }),
            dsl: doc.dsl(),
        }
    }
}

/// `POST /api/boards/{name}/ops` and `PUT /api/boards/{name}` reply.
#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize)]
pub struct OpReply {
    pub rev: u64,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub warnings: Vec<LineError>,
}

/// Error body for any refused request.
#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize)]
pub struct ErrorBody {
    pub code: String,
    pub detail: String,
}

pub fn hash_hex(hash: u64) -> String {
    format!("{hash:016x}")
}

#[cfg(test)]
mod tests {
    use super::*;
    use blockies_model::{Material, Scene};

    #[test]
    fn snapshot_round_trip_is_exact_and_small() {
        let mut w = World::new(SimSpec {
            width: 512,
            height: 256,
            scene: Scene::Calm,
            seed: 3,
        })
        .unwrap();
        for _ in 0..40 {
            w.step();
        }
        let s = Snapshot::of(&w);
        assert!(
            s.cells.len() < w.cells().len() / 20,
            "{} bytes",
            s.cells.len()
        );
        let mut back = s.world().unwrap();
        for _ in 0..50 {
            w.step();
            back.step();
        }
        assert_eq!(w.hash(), back.hash());
    }

    #[test]
    fn messages_are_tagged_json() {
        let m = ServerMsg::Tick {
            tick: 9,
            paints: vec![Paint {
                x: 1,
                y: 2,
                r: 3,
                material: Material::Sand,
            }],
        };
        let s = serde_json::to_string(&m).unwrap();
        assert_eq!(
            s,
            r#"{"t":"tick","tick":9,"paints":[{"x":1,"y":2,"r":3,"material":"sand"}]}"#
        );
        let c: ClientMsg =
            serde_json::from_str(r#"{"t":"op","id":1,"op":{"op":"step","to":"next"}}"#).unwrap();
        assert_eq!(
            c,
            ClientMsg::Op {
                id: 1,
                op: Op::Step { to: "next".into() }
            }
        );
    }
}
