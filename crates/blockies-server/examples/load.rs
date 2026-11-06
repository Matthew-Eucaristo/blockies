//! `cargo run --release -p blockies-server --example load`: a real server in-process,
//! N WebSocket clients on one board sending ops; reports ack latency and fan-out.
use blockies_ops::Op;
use blockies_protocol::{ClientMsg, ServerMsg};
use blockies_server::{Config, Hub, serve};
use blockies_store::SqliteStore;
use futures_util::{SinkExt, StreamExt};
use std::{sync::Arc, time::{Duration, Instant}};
use tokio_tungstenite::tungstenite::Message;

const WEB: &str = include_str!("../../blockies-dsl/examples/web.blk");

#[tokio::main]
async fn main() {
    let dir = std::env::temp_dir().join(format!("blockies-load-{}", std::process::id()));
    std::fs::create_dir_all(&dir).unwrap();
    let store = Arc::new(SqliteStore::open(dir.join("load.db")).unwrap());
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let addr = listener.local_addr().unwrap();
    tokio::spawn(serve(listener, Hub::new(store, Config::default()), std::future::pending()));
    let base = format!("http://{addr}/api/boards/web");
    tokio::task::spawn_blocking(move || ureq::put(&base).send(WEB).unwrap()).await.unwrap();

    for (clients, rate) in [(50usize, 20u64), (100, 10), (200, 10), (500, 4)] {
        let secs = 5;
        let mut tasks = Vec::new();
        for c in 0..clients {
            let url = format!("ws://{addr}/ws/web");
            tasks.push(tokio::spawn(async move {
                let (ws, _) = tokio_tungstenite::connect_async(url).await.unwrap();
                let (mut tx, mut rx) = ws.split();
                let mut sent = std::collections::HashMap::new();
                let (mut lat, mut patches, mut sent_n, mut dropped) = (Vec::new(), 0u64, 0u64, false);
                let mut tick = tokio::time::interval(Duration::from_millis(1000 / rate));
                let end = Instant::now() + Duration::from_secs(secs);
                let mut id = 0u64;
                loop {
                    tokio::select! {
                        _ = tick.tick(), if Instant::now() < end => {
                            id += 1;
                            let piece = ["browser", "req", "db", "cache", "load"][(id as usize + c) % 5];
                            let op = Op::Cycle { piece: piece.into() };
                            sent.insert(id, Instant::now());
                            let m = serde_json::to_string(&ClientMsg::Op { id, op }).unwrap();
                            if tx.send(Message::text(m)).await.is_err() {
                                dropped = true;
                                break;
                            }
                            sent_n += 1;
                        }
                        m = rx.next() => match m {
                            Some(Ok(Message::Text(t))) => match serde_json::from_str::<ServerMsg>(t.as_str()).unwrap() {
                                ServerMsg::Ack { id, .. } => lat.push(sent[&id].elapsed().as_micros() as u64),
                                ServerMsg::Patch { .. } => patches += 1,
                                _ => {}
                            },
                            Some(Ok(_)) => {}
                            _ => {
                                dropped = true;
                                break;
                            }
                        },
                        () = tokio::time::sleep_until((end + Duration::from_millis(500)).into()) => break,
                    }
                }
                (lat, patches, sent_n, dropped)
            }));
        }
        let (mut lat, mut patches, mut sent, mut dropped) = (Vec::new(), 0, 0, 0);
        for t in tasks {
            let (l, p, s, d) = t.await.unwrap();
            lat.extend(l);
            patches += p;
            sent += s;
            dropped += usize::from(d);
        }
        lat.sort_unstable();
        let q = |f: f64| lat[((lat.len() as f64 * f) as usize).min(lat.len() - 1)];
        println!(
            "{clients:>4} clients x {rate:>2} ops/s: sent {:>5.0}/s acked {:>5.0}/s, {:>7.0} patches/s delivered, ack p50 {} µs p99 {} µs max {} µs, {dropped} dropped",
            sent as f64 / secs as f64,
            lat.len() as f64 / secs as f64,
            patches as f64 / secs as f64,
            q(0.5),
            q(0.99),
            lat[lat.len() - 1]
        );
    }
    std::fs::remove_dir_all(dir).ok();
}
