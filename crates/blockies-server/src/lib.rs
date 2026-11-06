//! The Blockies server: owns boards and lets every runtime watch and change them.
//!
//! | Route | Purpose |
//! |---|---|
//! | `GET /ws/{board}` | WebSocket, protocol v1 (`blockies-protocol`): live patches and lockstep ticks |
//! | `GET /api/boards` | list boards |
//! | `GET /api/boards/{board}` | agent view: pieces with state names, step, caption, DSL |
//! | `PUT /api/boards/{board}` | create or replace from DSL (body is the DSL text) |
//! | `POST /api/boards/{board}/ops` | apply one op (JSON, see `blockies_ops::Op`) |
//!
//! Every change, from any surface, goes through the board actor (see [`board`]).

mod board;
mod persist;

use axum::{
    Json, Router,
    extract::{
        Path, State, WebSocketUpgrade,
        ws::{Message, Utf8Bytes, WebSocket},
    },
    http::StatusCode,
    response::{IntoResponse, Response},
    routing::get,
};
use blockies_ops::{Doc, Op, OpError};
use blockies_protocol::{BoardView, ClientMsg, ErrorBody, OpReply};
use blockies_store::Store;
use board::{Board, Cmd};
use futures_util::{SinkExt, StreamExt};
use persist::Persister;
use std::{
    collections::HashMap,
    sync::{
        Arc,
        atomic::{AtomicU32, Ordering},
    },
};
use tokio::sync::{Mutex, mpsc, oneshot};
use tokio::time::Duration;

pub use board::{MAILBOX, OUTBOX};

/// Server settings.
#[derive(Clone, Debug)]
pub struct Config {
    /// Lockstep tick length for block fields.
    pub tick: Duration,
}

impl Default for Config {
    fn default() -> Self {
        Self {
            tick: Duration::from_millis(33),
        }
    }
}

#[derive(Clone)]
pub struct Hub {
    inner: Arc<Inner>,
}

struct Inner {
    boards: Mutex<HashMap<String, mpsc::Sender<Cmd>>>,
    store: Arc<dyn Store>,
    persist: Persister,
    next_client: AtomicU32,
    config: Config,
}

impl std::fmt::Debug for Hub {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("Hub")
            .field("config", &self.inner.config)
            .finish_non_exhaustive()
    }
}

/// A refused request: HTTP status plus a machine-readable code.
#[derive(Debug)]
pub struct ApiError {
    status: StatusCode,
    body: ErrorBody,
}

impl ApiError {
    fn new(status: StatusCode, code: &str, detail: impl Into<String>) -> Self {
        Self {
            status,
            body: ErrorBody {
                code: code.to_string(),
                detail: detail.into(),
            },
        }
    }
}

impl From<OpError> for ApiError {
    fn from(e: OpError) -> Self {
        Self::new(StatusCode::UNPROCESSABLE_ENTITY, e.code, e.detail)
    }
}

impl IntoResponse for ApiError {
    fn into_response(self) -> Response {
        (self.status, Json(self.body)).into_response()
    }
}

/// Board names: 1-64 of `a-z 0-9 - _`.
pub fn valid_name(name: &str) -> bool {
    (1..=64).contains(&name.len())
        && name
            .bytes()
            .all(|b| b.is_ascii_lowercase() || b.is_ascii_digit() || b == b'-' || b == b'_')
}

fn check_name(name: &str) -> Result<(), ApiError> {
    if valid_name(name) {
        Ok(())
    } else {
        Err(ApiError::new(
            StatusCode::BAD_REQUEST,
            "bad_name",
            "board names use 1-64 of a-z, 0-9, - and _",
        ))
    }
}

fn gone() -> ApiError {
    ApiError::new(
        StatusCode::SERVICE_UNAVAILABLE,
        "board_gone",
        "board actor stopped",
    )
}

impl Hub {
    pub fn new(store: Arc<dyn Store>, config: Config) -> Self {
        let persist = Persister::start(store.clone());
        Self {
            inner: Arc::new(Inner {
                boards: Mutex::new(HashMap::new()),
                store,
                persist,
                next_client: AtomicU32::new(0),
                config,
            }),
        }
    }

    /// The actor for `name`, loading it from the store or (if `create`) starting it empty.
    async fn board(&self, name: &str, create: bool) -> Result<Option<mpsc::Sender<Cmd>>, ApiError> {
        let mut boards = self.inner.boards.lock().await;
        if let Some(tx) = boards.get(name).filter(|tx| !tx.is_closed()) {
            return Ok(Some(tx.clone()));
        }
        let store = self.inner.store.clone();
        let key = name.to_string();
        let saved = tokio::task::spawn_blocking(move || store.load(&key))
            .await
            .map_err(|e| ApiError::new(StatusCode::INTERNAL_SERVER_ERROR, "store", e.to_string()))?
            .map_err(|e| {
                ApiError::new(StatusCode::INTERNAL_SERVER_ERROR, "store", e.to_string())
            })?;
        let doc = match saved {
            Some(s) => Doc::restore(&s.dsl, &s.states, s.step, s.rev),
            None if create => Doc::new(blockies_model::Board::default()),
            None => return Ok(None),
        };
        let (tx, rx) = mpsc::channel(MAILBOX);
        let actor = Board::new(
            name.to_string(),
            doc,
            self.inner.persist.clone(),
            self.inner.config.tick,
        );
        tokio::spawn(actor.run(rx));
        boards.insert(name.to_string(), tx.clone());
        Ok(Some(tx))
    }

    async fn existing(&self, name: &str) -> Result<mpsc::Sender<Cmd>, ApiError> {
        check_name(name)?;
        self.board(name, false).await?.ok_or_else(|| {
            ApiError::new(
                StatusCode::NOT_FOUND,
                "no_board",
                format!("no board \"{name}\"; create it with PUT"),
            )
        })
    }

    async fn op(&self, tx: &mpsc::Sender<Cmd>, op: Op) -> Result<OpReply, ApiError> {
        let (reply, rx) = oneshot::channel();
        tx.send(Cmd::Op {
            client: 0,
            id: 0,
            op,
            reply: Some(reply),
        })
        .await
        .map_err(|_| gone())?;
        Ok(rx.await.map_err(|_| gone())??)
    }
}

pub fn router(hub: Hub) -> Router {
    Router::new()
        .route(
            "/",
            get(|| async { "blockies server: see /api/boards and /ws/{board}\n" }),
        )
        .route("/api/boards", get(list))
        .route("/api/boards/{name}", get(view).put(put_board))
        .route("/api/boards/{name}/ops", axum::routing::post(post_op))
        .route("/ws/{name}", get(ws))
        .with_state(hub)
}

async fn list(State(hub): State<Hub>) -> Result<Json<Vec<blockies_store::BoardInfo>>, ApiError> {
    let store = hub.inner.store.clone();
    let list = tokio::task::spawn_blocking(move || store.list())
        .await
        .map_err(|e| ApiError::new(StatusCode::INTERNAL_SERVER_ERROR, "store", e.to_string()))?
        .map_err(|e| ApiError::new(StatusCode::INTERNAL_SERVER_ERROR, "store", e.to_string()))?;
    Ok(Json(list))
}

async fn view(
    State(hub): State<Hub>,
    Path(name): Path<String>,
) -> Result<Json<BoardView>, ApiError> {
    let tx = hub.existing(&name).await?;
    let (reply, rx) = oneshot::channel();
    tx.send(Cmd::View { reply }).await.map_err(|_| gone())?;
    Ok(Json(rx.await.map_err(|_| gone())?))
}

async fn put_board(
    State(hub): State<Hub>,
    Path(name): Path<String>,
    dsl: String,
) -> Result<Json<OpReply>, ApiError> {
    check_name(&name)?;
    let tx = hub.board(&name, true).await?.ok_or_else(gone)?;
    Ok(Json(hub.op(&tx, Op::Load { dsl }).await?))
}

async fn post_op(
    State(hub): State<Hub>,
    Path(name): Path<String>,
    Json(op): Json<Op>,
) -> Result<Json<OpReply>, ApiError> {
    let tx = hub.existing(&name).await?;
    Ok(Json(hub.op(&tx, op).await?))
}

async fn ws(
    State(hub): State<Hub>,
    Path(name): Path<String>,
    up: WebSocketUpgrade,
) -> Result<Response, ApiError> {
    check_name(&name)?;
    let tx = hub.board(&name, true).await?.ok_or_else(gone)?;
    Ok(up.on_upgrade(move |socket| client(socket, tx, hub)))
}

async fn client(socket: WebSocket, board: mpsc::Sender<Cmd>, hub: Hub) {
    let id = hub.inner.next_client.fetch_add(1, Ordering::Relaxed) + 1;
    let (out_tx, mut out_rx) = mpsc::channel::<Utf8Bytes>(OUTBOX);
    if board
        .send(Cmd::Join {
            client: id,
            out: out_tx,
        })
        .await
        .is_err()
    {
        return;
    }
    let (mut sink, mut stream) = socket.split();
    let mut writer = tokio::spawn(async move {
        while let Some(f) = out_rx.recv().await {
            if sink.send(Message::Text(f)).await.is_err() {
                return;
            }
        }
        let _ = sink.close().await;
    });
    let reader = async {
        while let Some(Ok(msg)) = stream.next().await {
            let text = match msg {
                Message::Text(t) => t,
                Message::Close(_) => break,
                _ => continue,
            };
            let cmd = match serde_json::from_str::<ClientMsg>(text.as_str()) {
                Ok(ClientMsg::Op { id: op_id, op }) => Cmd::Op {
                    client: id,
                    id: op_id,
                    op,
                    reply: None,
                },
                Ok(ClientMsg::Hash { tick, hash }) => Cmd::Hash {
                    client: id,
                    tick,
                    hash,
                },
                Ok(ClientMsg::Resync) => Cmd::Resync { client: id },
                Err(e) => {
                    tracing::debug!(client = id, "bad message: {e}");
                    break;
                }
            };
            if board.send(cmd).await.is_err() {
                break;
            }
        }
    };
    tokio::select! {
        () = reader => {}
        _ = &mut writer => {}
    }
    let _ = board.send(Cmd::Leave { client: id }).await;
    writer.abort();
}

/// Serves until `shutdown` resolves.
pub async fn serve(
    listener: tokio::net::TcpListener,
    hub: Hub,
    shutdown: impl std::future::Future<Output = ()> + Send + 'static,
) -> std::io::Result<()> {
    use axum::serve::ListenerExt as _;
    let listener = listener.tap_io(|tcp| {
        let _ = tcp.set_nodelay(true);
    });
    axum::serve(listener, router(hub))
        .with_graceful_shutdown(shutdown)
        .await
}
