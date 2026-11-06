//! End to end: a real server on a random port, HTTP like an agent, WebSockets like runtimes.

use blockies_model::Material;
use blockies_ops::{Doc, Op};
use blockies_protocol::{BoardView, ClientMsg, HASH_EVERY, OpReply, ServerMsg, hash_hex};
use blockies_server::{Config, Hub, serve};
use blockies_sim::World;
use blockies_store::SqliteStore;
use futures_util::{SinkExt, StreamExt};
use std::{collections::BTreeMap, sync::Arc, time::Duration};
use tokio_tungstenite::tungstenite::Message;

const WEB: &str = include_str!("../../blockies-dsl/examples/web.blk");
const SANDBOX: &str = include_str!("../../blockies-dsl/examples/sandbox.blk");

async fn start() -> String {
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let addr = listener.local_addr().unwrap();
    let hub = Hub::new(
        Arc::new(SqliteStore::memory().unwrap()),
        Config {
            tick: Duration::from_millis(5),
        },
    );
    tokio::spawn(serve(listener, hub, std::future::pending()));
    format!("127.0.0.1:{}", addr.port())
}

async fn http(method: &'static str, url: String, body: Option<String>) -> (u16, String) {
    tokio::task::spawn_blocking(move || {
        let agent = ureq::Agent::config_builder()
            .http_status_as_error(false)
            .build()
            .new_agent();
        let mut res = match (method, body) {
            ("GET", _) => agent.get(&url).call(),
            ("PUT", Some(b)) => agent.put(&url).send(b),
            ("POST", Some(b)) => agent
                .post(&url)
                .header("content-type", "application/json")
                .send(b),
            _ => unreachable!(),
        }
        .unwrap();
        (
            res.status().as_u16(),
            res.body_mut().read_to_string().unwrap(),
        )
    })
    .await
    .unwrap()
}

type Ws =
    tokio_tungstenite::WebSocketStream<tokio_tungstenite::MaybeTlsStream<tokio::net::TcpStream>>;

async fn connect(addr: &str, board: &str) -> Ws {
    tokio_tungstenite::connect_async(format!("ws://{addr}/ws/{board}"))
        .await
        .unwrap()
        .0
}

async fn recv(ws: &mut Ws) -> ServerMsg {
    loop {
        let msg = tokio::time::timeout(Duration::from_secs(5), ws.next())
            .await
            .expect("timeout")
            .unwrap()
            .unwrap();
        if let Message::Text(t) = msg {
            return serde_json::from_str(t.as_str()).unwrap();
        }
    }
}

async fn send(ws: &mut Ws, msg: &ClientMsg) {
    ws.send(Message::text(serde_json::to_string(msg).unwrap()))
        .await
        .unwrap();
}

#[tokio::test]
async fn agents_over_http_and_runtimes_over_websocket() {
    let addr = start().await;
    let base = format!("http://{addr}/api/boards");

    let (code, body) = http("GET", format!("{base}/web"), None).await;
    assert_eq!(code, 404, "{body}");
    let (code, body) = http("PUT", format!("{base}/web"), Some(WEB.into())).await;
    assert_eq!(code, 200, "{body}");

    let mut a = connect(&addr, "web").await;
    let mut b = connect(&addr, "web").await;
    let ServerMsg::Welcome {
        dsl, states, you, ..
    } = recv(&mut a).await
    else {
        panic!("welcome")
    };
    let mut mirror = Doc::restore(&dsl, &states, None, 1);
    assert!(matches!(recv(&mut b).await, ServerMsg::Welcome { .. }));

    // Runtime A changes a piece: both runtimes get the patch, A gets the ack.
    send(
        &mut a,
        &ClientMsg::Op {
            id: 7,
            op: Op::Set {
                piece: "load".into(),
                state: "spike".into(),
            },
        },
    )
    .await;
    let ServerMsg::Patch { by, patches, rev } = recv(&mut a).await else {
        panic!("patch")
    };
    assert_eq!(by, you);
    for p in &patches {
        mirror.apply_patch(p, rev);
    }
    assert!(matches!(recv(&mut a).await, ServerMsg::Ack { id: 7, .. }));
    assert!(matches!(recv(&mut b).await, ServerMsg::Patch { .. }));

    // A bad op is refused with a code, and only the sender hears about it.
    send(
        &mut b,
        &ClientMsg::Op {
            id: 8,
            op: Op::Set {
                piece: "api".into(),
                state: "busy".into(),
            },
        },
    )
    .await;
    let ServerMsg::Reject { id, code, .. } = recv(&mut b).await else {
        panic!("reject")
    };
    assert_eq!((id, code.as_str()), (8, "driven"));

    // An agent moves the token over HTTP; the runtimes see it live.
    let (code, body) = http(
        "POST",
        format!("{base}/web/ops"),
        Some(r#"{"op":"move","piece":"req","to":"db"}"#.into()),
    )
    .await;
    assert_eq!(code, 200, "{body}");
    let reply: OpReply = serde_json::from_str(&body).unwrap();
    let ServerMsg::Patch { patches, rev, .. } = recv(&mut a).await else {
        panic!("patch")
    };
    assert_eq!(rev, reply.rev);
    for p in &patches {
        mirror.apply_patch(p, rev);
    }

    let (code, body) = http(
        "POST",
        format!("{base}/web/ops"),
        Some(r#"{"op":"set","piece":"api","state":"busy"}"#.into()),
    )
    .await;
    assert_eq!(code, 422);
    assert!(body.contains("\"code\":\"driven\""), "{body}");

    let (_, body) = http("GET", format!("{base}/web"), None).await;
    let view: BoardView = serde_json::from_str(&body).unwrap();
    let req = view.pieces.iter().find(|p| p.id == "req").unwrap();
    assert_eq!(req.state, "3");
    assert_eq!(
        mirror.states()[mirror.board().index("req").unwrap()],
        3,
        "runtime mirror matches the owner"
    );

    let (_, body) = http("GET", base.clone(), None).await;
    assert!(body.contains("\"name\":\"web\""), "{body}");
    let (code, _) = http("PUT", format!("{base}/Bad%20Name"), Some(WEB.into())).await;
    assert_eq!(code, 400);
}

/// A runtime: restores the snapshot and plays ticks exactly like the owner.
struct Runtime {
    ws: Ws,
    world: Option<World>,
    hashes: BTreeMap<u32, u64>,
}

impl Runtime {
    async fn join(addr: &str, board: &str) -> Self {
        let mut ws = connect(addr, board).await;
        let ServerMsg::Welcome { sim, .. } = recv(&mut ws).await else {
            panic!("welcome")
        };
        Self {
            ws,
            world: Some(sim.expect("board has a sim").world().unwrap()),
            hashes: BTreeMap::new(),
        }
    }

    /// Plays until the world reaches `until`, reporting hashes like a real client.
    async fn play(&mut self, until: u32) {
        while self.world.as_ref().unwrap().tick() < until {
            match recv(&mut self.ws).await {
                ServerMsg::Tick { tick, paints } => {
                    let w = self.world.as_mut().unwrap();
                    assert_eq!(w.tick(), tick, "ticks arrive in order");
                    for p in &paints {
                        w.paint(p.x, p.y, p.r, p.material);
                    }
                    w.step();
                    if w.tick().is_multiple_of(HASH_EVERY) {
                        let (tick, hash) = (w.tick(), w.hash());
                        self.hashes.insert(tick, hash);
                        send(
                            &mut self.ws,
                            &ClientMsg::Hash {
                                tick,
                                hash: hash_hex(hash),
                            },
                        )
                        .await;
                    }
                }
                ServerMsg::Snapshot { .. } => panic!("owner reported a desync"),
                _ => {}
            }
        }
    }
}

#[tokio::test]
async fn block_field_runs_in_lockstep() {
    let addr = start().await;
    let (code, body) = http(
        "PUT",
        format!("http://{addr}/api/boards/sand"),
        Some(SANDBOX.into()),
    )
    .await;
    assert_eq!(code, 200, "{body}");

    let mut a = Runtime::join(&addr, "sand").await;
    a.play(20).await;
    // B joins late, from a snapshot taken mid-run.
    let mut b = Runtime::join(&addr, "sand").await;

    for (i, m) in [
        Material::Sand,
        Material::Water,
        Material::Wall,
        Material::Empty,
    ]
    .into_iter()
    .enumerate()
    {
        let op = Op::Paint {
            x: 100 + 80 * i as i32,
            y: 30,
            r: 10,
            material: m,
        };
        send(&mut a.ws, &ClientMsg::Op { id: i as u64, op }).await;
    }
    a.play(150).await;
    b.play(150).await;

    let common: Vec<_> = a
        .hashes
        .keys()
        .filter(|t| b.hashes.contains_key(t))
        .collect();
    assert!(common.len() >= 3, "{common:?}");
    for t in common {
        assert_eq!(a.hashes[t], b.hashes[t], "tick {t}");
    }
    let (_, body) = http("GET", format!("http://{addr}/api/boards/sand"), None).await;
    let view: BoardView = serde_json::from_str(&body).unwrap();
    assert!(view.sim.unwrap().tick >= 150);
}
