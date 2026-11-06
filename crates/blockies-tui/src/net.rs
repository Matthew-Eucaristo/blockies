//! Live mode: a background thread owns the WebSocket (with reconnects) and talks to the UI
//! thread through channels, so the UI never blocks on the network.

use blockies_protocol::{ClientMsg, ServerMsg};
use futures_util::{SinkExt, StreamExt};
use std::sync::mpsc as std_mpsc;
use std::time::Duration;
use tokio::sync::mpsc;
use tokio_tungstenite::tungstenite::Message;

#[derive(Debug)]
pub enum NetEvent {
    Connected,
    Msg(Box<ServerMsg>),
    Closed(String),
}

#[derive(Debug)]
pub struct Net {
    tx: mpsc::UnboundedSender<ClientMsg>,
    rx: std_mpsc::Receiver<NetEvent>,
}

impl Net {
    pub fn connect(url: String) -> Self {
        let (tx, mut out_rx) = mpsc::unbounded_channel::<ClientMsg>();
        let (ev_tx, rx) = std_mpsc::channel();
        let target = url;
        std::thread::Builder::new()
            .name("blockies-net".into())
            .spawn(move || {
                let rt = tokio::runtime::Builder::new_current_thread().enable_all().build().expect("tokio runtime");
                rt.block_on(async move {
                    loop {
                        match tokio_tungstenite::connect_async(target.as_str()).await {
                            Ok((ws, _)) => {
                                if ev_tx.send(NetEvent::Connected).is_err() {
                                    return;
                                }
                                let (mut sink, mut stream) = ws.split();
                                let why = loop {
                                    tokio::select! {
                                        out = out_rx.recv() => match out {
                                            Some(m) => {
                                                let text = serde_json::to_string(&m).expect("client messages serialise");
                                                if let Err(e) = sink.send(Message::text(text)).await {
                                                    break e.to_string();
                                                }
                                            }
                                            None => return, // UI closed
                                        },
                                        msg = stream.next() => match msg {
                                            Some(Ok(Message::Text(t))) => match serde_json::from_str::<ServerMsg>(t.as_str()) {
                                                Ok(m) => {
                                                    if ev_tx.send(NetEvent::Msg(Box::new(m))).is_err() {
                                                        return;
                                                    }
                                                }
                                                Err(e) => break format!("bad message from server: {e}"),
                                            },
                                            Some(Ok(_)) => {}
                                            Some(Err(e)) => break e.to_string(),
                                            None => break "server closed the connection".into(),
                                        },
                                    }
                                };
                                if ev_tx.send(NetEvent::Closed(why)).is_err() {
                                    return;
                                }
                            }
                            Err(e) => {
                                if ev_tx.send(NetEvent::Closed(e.to_string())).is_err() {
                                    return;
                                }
                            }
                        }
                        tokio::time::sleep(Duration::from_secs(1)).await;
                        // Messages queued while offline belong to the old session.
                        while out_rx.try_recv().is_ok() {}
                    }
                });
            })
            .expect("spawn net thread");
        Self { tx, rx }
    }

    pub fn send(&self, msg: ClientMsg) {
        let _ = self.tx.send(msg);
    }

    pub fn poll(&self) -> Option<NetEvent> {
        self.rx.try_recv().ok()
    }
}
