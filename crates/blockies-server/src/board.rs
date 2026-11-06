//! The board actor: the only owner of one board's state.
//!
//! It validates ops through `blockies_ops::Doc`, broadcasts patches, and, when the board has a
//! block field, runs the lockstep clock: paints are queued, and every tick it applies them in
//! order, steps its own copy of the sim and broadcasts `Tick { tick, paints }`. Its copy is the
//! reference for joiners (snapshot) and for clients' hash checks.

use crate::persist::Persister;
use axum::extract::ws::Utf8Bytes;
use blockies_ops::{Doc, Op, OpError, Paint};
use blockies_protocol::{BoardView, HASH_EVERY, OpReply, PROTOCOL, ServerMsg, Snapshot, hash_hex};
use blockies_sim::World;
use blockies_store::Saved;
use std::collections::VecDeque;
use tokio::sync::{mpsc, oneshot};
use tokio::time::{Duration, Interval, MissedTickBehavior};

/// Per-client outbox (frames). A client this far behind is dropped; it reconnects and resyncs.
pub const OUTBOX: usize = 512;
pub const MAILBOX: usize = 4096;
/// Server-side hashes kept for checking clients (ticks = `HASH_EVERY * HASHES`).
const HASHES: usize = 64;

pub type Reply = oneshot::Sender<Result<OpReply, OpError>>;

#[derive(Debug)]
pub enum Cmd {
    Join {
        client: u32,
        out: mpsc::Sender<Utf8Bytes>,
    },
    Leave {
        client: u32,
    },
    /// `reply` for HTTP callers; WebSocket callers get `Ack` / `Reject` on their socket.
    Op {
        client: u32,
        id: u64,
        op: Op,
        reply: Option<Reply>,
    },
    Hash {
        client: u32,
        tick: u32,
        hash: String,
    },
    Resync {
        client: u32,
    },
    View {
        reply: oneshot::Sender<BoardView>,
    },
}

fn frame(msg: &ServerMsg) -> Utf8Bytes {
    serde_json::to_string(msg)
        .expect("protocol messages serialise")
        .into()
}

struct Sub {
    id: u32,
    out: mpsc::Sender<Utf8Bytes>,
}

pub struct Board {
    name: String,
    doc: Doc,
    world: Option<World>,
    subs: Vec<Sub>,
    paints: Vec<Paint>,
    hashes: VecDeque<(u32, u64)>,
    persist: Persister,
    tick_every: Duration,
}

impl Board {
    pub fn new(name: String, doc: Doc, persist: Persister, tick_every: Duration) -> Self {
        let world = make_world(&doc);
        Self {
            name,
            doc,
            world,
            subs: Vec::new(),
            paints: Vec::new(),
            hashes: VecDeque::new(),
            persist,
            tick_every,
        }
    }

    pub async fn run(mut self, mut inbox: mpsc::Receiver<Cmd>) {
        let mut clock: Option<Interval> = None;
        loop {
            // The clock runs only while someone is watching a board that has a block field.
            let want = self.world.is_some() && !self.subs.is_empty();
            if want != clock.is_some() {
                clock = want.then(|| {
                    let mut i = tokio::time::interval(self.tick_every);
                    i.set_missed_tick_behavior(MissedTickBehavior::Delay);
                    i
                });
            }
            tokio::select! {
                cmd = inbox.recv() => match cmd {
                    Some(cmd) => self.handle(cmd),
                    None => return,
                },
                () = next_tick(&mut clock) => self.tick(),
            }
        }
    }

    fn handle(&mut self, cmd: Cmd) {
        match cmd {
            Cmd::Join { client, out } => {
                let hello = ServerMsg::Welcome {
                    protocol: PROTOCOL,
                    board: self.name.clone(),
                    you: client,
                    rev: self.doc.rev(),
                    dsl: self.doc.dsl(),
                    states: self.doc.states().to_vec(),
                    step: self.doc.step(),
                    sim: self.world.as_ref().map(Snapshot::of),
                };
                if out.try_send(frame(&hello)).is_ok() {
                    self.subs.push(Sub { id: client, out });
                }
            }
            Cmd::Leave { client } => self.subs.retain(|s| s.id != client),
            Cmd::Op {
                client,
                id,
                op,
                reply,
            } => {
                let result = self.apply(client, &op);
                match reply {
                    Some(r) => {
                        let _ = r.send(result);
                    }
                    None => {
                        let msg = match result {
                            Ok(ok) => ServerMsg::Ack {
                                id,
                                rev: ok.rev,
                                warnings: ok.warnings,
                            },
                            Err(e) => ServerMsg::Reject {
                                id,
                                code: e.code.to_string(),
                                detail: e.detail,
                            },
                        };
                        self.send_to(client, &msg);
                    }
                }
            }
            Cmd::Hash { client, tick, hash } => {
                let ours = self
                    .hashes
                    .iter()
                    .find(|(t, _)| *t == tick)
                    .map(|(_, h)| hash_hex(*h));
                if ours.is_some_and(|h| h != hash) {
                    tracing::warn!(board = %self.name, client, tick, "lockstep desync; sending snapshot");
                    self.send_snapshot(client);
                }
            }
            Cmd::Resync { client } => self.send_snapshot(client),
            Cmd::View { reply } => {
                let _ = reply.send(BoardView::new(&self.name, &self.doc, self.world.as_ref()));
            }
        }
    }

    fn apply(&mut self, client: u32, op: &Op) -> Result<OpReply, OpError> {
        let applied = self.doc.apply(op)?;
        let rev = self.doc.rev();
        if let Some(p) = applied.paint {
            self.paints.push(p);
        }
        if !applied.patches.is_empty() {
            self.broadcast(&ServerMsg::Patch {
                rev,
                by: client,
                patches: applied.patches,
            });
        }
        if matches!(op, Op::Load { .. })
            && self.world.as_ref().map(World::spec) != self.doc.board().sim
        {
            // New or changed block field: everyone restarts from the owner's snapshot.
            self.world = make_world(&self.doc);
            self.paints.clear();
            self.hashes.clear();
            if let Some(w) = &self.world {
                self.broadcast(&ServerMsg::Snapshot {
                    sim: Snapshot::of(w),
                });
            }
        }
        if !matches!(op, Op::Paint { .. }) {
            self.persist.commit(
                Saved {
                    name: self.name.clone(),
                    dsl: self.doc.dsl(),
                    states: self.doc.states().to_vec(),
                    step: self.doc.step(),
                    rev,
                },
                serde_json::to_string(op).ok(),
            );
        }
        Ok(OpReply {
            rev,
            warnings: applied.warnings,
        })
    }

    fn tick(&mut self) {
        let Some(w) = &mut self.world else { return };
        let tick = w.tick();
        let paints = std::mem::take(&mut self.paints);
        for p in &paints {
            w.paint(p.x, p.y, p.r, p.material);
        }
        w.step();
        if w.tick().is_multiple_of(HASH_EVERY) {
            if self.hashes.len() == HASHES {
                self.hashes.pop_front();
            }
            self.hashes.push_back((w.tick(), w.hash()));
        }
        self.broadcast(&ServerMsg::Tick { tick, paints });
    }

    fn send_snapshot(&mut self, client: u32) {
        if let Some(w) = &self.world {
            let msg = ServerMsg::Snapshot {
                sim: Snapshot::of(w),
            };
            self.send_to(client, &msg);
        }
    }

    fn send_to(&mut self, client: u32, msg: &ServerMsg) {
        if let Some(s) = self.subs.iter().find(|s| s.id == client)
            && s.out.try_send(frame(msg)).is_err()
        {
            self.subs.retain(|s| s.id != client);
        }
    }

    /// Serialised once; every subscriber gets a refcounted handle to the same bytes.
    /// A subscriber whose outbox is full is dropped instead of slowing the board down.
    fn broadcast(&mut self, msg: &ServerMsg) {
        let f = frame(msg);
        self.subs.retain(|s| s.out.try_send(f.clone()).is_ok());
    }
}

fn make_world(doc: &Doc) -> Option<World> {
    doc.board().sim.and_then(|spec| World::new(spec).ok())
}

async fn next_tick(clock: &mut Option<Interval>) {
    match clock {
        Some(c) => {
            c.tick().await;
        }
        None => std::future::pending().await,
    }
}
