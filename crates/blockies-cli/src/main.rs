//! `blockies`: one binary for people and agents.
//!
//! Agents drive boards from the shell (`blockies move web req db`); every runtime watching that
//! board animates the change. With `--json`, output is one JSON value and errors are
//! `{"error":{"code":..,"detail":..}}`.
//!
//! Exit codes: 0 ok, 1 refused (bad op, DSL problems), 2 usage, 3 server unreachable.

use anyhow::Context as _;
use blockies_model::Material;
use blockies_ops::Op;
use blockies_protocol::{BoardView, ErrorBody, OpReply};
use clap::{Parser, Subcommand};
use std::io::Read as _;
use std::path::PathBuf;
use std::process::ExitCode;

const DEFAULT_SERVER: &str = "http://127.0.0.1:4318";

#[derive(Parser)]
#[command(
    name = "blockies",
    version,
    about = "A living whiteboard: pieces with declared states, animated everywhere."
)]
struct Cli {
    /// Server for board commands.
    #[arg(long, global = true, env = "BLOCKIES_SERVER", default_value = DEFAULT_SERVER)]
    server: String,
    /// Machine-readable output.
    #[arg(long, global = true)]
    json: bool,
    #[command(subcommand)]
    cmd: Cmd,
}

#[derive(Subcommand)]
enum Cmd {
    /// Run the server (boards persist in SQLite).
    Serve {
        #[arg(long, default_value = "127.0.0.1:4318")]
        addr: String,
        /// SQLite file.
        #[arg(long, default_value = "blockies.db")]
        db: PathBuf,
        /// Keep boards in memory only.
        #[arg(long)]
        memory: bool,
        /// Lockstep tick for block fields, in milliseconds.
        #[arg(long, default_value_t = 33)]
        tick_ms: u64,
    },
    /// Open the terminal runtime: a .blk file or example locally, or a server board live.
    Tui {
        /// A .blk file, or a board name on --server.
        target: Option<String>,
        /// Run a built-in example locally (web, water, sprint, sandbox).
        #[arg(long, conflicts_with = "target")]
        example: Option<String>,
    },
    /// Create or replace a board from DSL (a file, or - for stdin).
    New { board: String, file: String },
    /// Show a board: pieces, states, step.
    Show { board: String },
    /// List boards.
    Ls,
    /// Put a piece in a state.
    Set {
        board: String,
        piece: String,
        state: String,
    },
    /// Move a token to a piece on its route.
    Move {
        board: String,
        piece: String,
        to: String,
    },
    /// Advance a piece to its next state.
    Cycle { board: String, piece: String },
    /// Walk the steps: next, prev, start or a step number.
    Step { board: String, to: String },
    /// Paint a disc into the block field.
    Paint {
        board: String,
        x: i32,
        y: i32,
        #[arg(long, default_value_t = 4)]
        r: u16,
        /// sand, water, wall or empty.
        #[arg(long, default_value = "sand")]
        material: String,
    },
    /// Check DSL locally and list problems per line (a file, or - for stdin).
    Check { file: String },
    /// Print DSL in canonical form.
    Fmt { file: String },
    /// Print the instructions that teach any LLM the whole vocabulary.
    Prompt,
    /// List built-in examples, or print one.
    Examples { name: Option<String> },
}

/// A failure with an exit code and a machine-readable code.
struct Fail {
    exit: u8,
    code: String,
    detail: String,
}

impl Fail {
    fn new(exit: u8, code: &str, detail: impl Into<String>) -> Self {
        Self {
            exit,
            code: code.to_string(),
            detail: detail.into(),
        }
    }
}

impl From<anyhow::Error> for Fail {
    fn from(e: anyhow::Error) -> Self {
        Self::new(1, "error", format!("{e:#}"))
    }
}

type Out = Result<(), Fail>;

fn main() -> ExitCode {
    let cli = Cli::parse();
    let json = cli.json;
    match run(cli) {
        Ok(()) => ExitCode::SUCCESS,
        Err(f) if f.code.is_empty() => ExitCode::from(f.exit),
        Err(f) => {
            if json {
                println!(
                    "{}",
                    serde_json::json!({ "error": { "code": f.code, "detail": f.detail } })
                );
            } else {
                eprintln!("error: {} ({})", f.detail, f.code);
            }
            ExitCode::from(f.exit)
        }
    }
}

fn read_input(file: &str) -> Result<String, Fail> {
    if file == "-" {
        let mut s = String::new();
        std::io::stdin()
            .read_to_string(&mut s)
            .map_err(|e| Fail::new(1, "io", e.to_string()))?;
        Ok(s)
    } else {
        std::fs::read_to_string(file).map_err(|e| Fail::new(1, "io", format!("{file}: {e}")))
    }
}

fn run(cli: Cli) -> Out {
    let api = Api {
        base: cli.server.trim_end_matches('/').to_string(),
    };
    let json = cli.json;
    match cli.cmd {
        Cmd::Serve {
            addr,
            db,
            memory,
            tick_ms,
        } => serve(&addr, (!memory).then_some(db), tick_ms),
        Cmd::Tui {
            target,
            example: ex,
        } => {
            let source = match (target, ex) {
                (_, Some(name)) => blockies_tui::Source::Text {
                    dsl: example(&name)?.to_string(),
                    name,
                },
                (Some(t), None) if std::path::Path::new(&t).is_file() => {
                    blockies_tui::Source::File(t.into())
                }
                (Some(board), None) => blockies_tui::Source::Live {
                    url: api.ws_url(&board),
                },
                (None, None) => blockies_tui::Source::Text {
                    name: "web".into(),
                    dsl: example("web")?.to_string(),
                },
            };
            blockies_tui::run(source).map_err(Fail::from)
        }
        Cmd::New { board, file } => {
            let reply: OpReply = api.call(
                "PUT",
                &format!("/api/boards/{board}"),
                Some(read_input(&file)?),
                None,
            )?;
            print_reply(&reply, json, &format!("{board} saved"));
            Ok(())
        }
        Cmd::Show { board } => {
            let v: BoardView = api.call("GET", &format!("/api/boards/{board}"), None, None)?;
            if json {
                println!("{}", serde_json::to_string(&v).expect("serialise"));
            } else {
                print_view(&v);
            }
            Ok(())
        }
        Cmd::Ls => {
            let list: Vec<blockies_store::BoardInfo> =
                api.call("GET", "/api/boards", None, None)?;
            if json {
                println!("{}", serde_json::to_string(&list).expect("serialise"));
            } else {
                for b in list {
                    println!("{:<24} rev {:<6} {}", b.name, b.rev, b.updated);
                }
            }
            Ok(())
        }
        Cmd::Set {
            board,
            piece,
            state,
        } => api.op(&board, &Op::Set { piece, state }, json),
        Cmd::Move { board, piece, to } => api.op(&board, &Op::Move { piece, to }, json),
        Cmd::Cycle { board, piece } => api.op(&board, &Op::Cycle { piece }, json),
        Cmd::Step { board, to } => api.op(&board, &Op::Step { to }, json),
        Cmd::Paint {
            board,
            x,
            y,
            r,
            material,
        } => {
            let material = Material::parse(&material).ok_or_else(|| {
                Fail::new(
                    1,
                    "bad_material",
                    format!("\"{material}\": use sand, water, wall or empty"),
                )
            })?;
            api.op(&board, &Op::Paint { x, y, r, material }, json)
        }
        Cmd::Check { file } => {
            let p = blockies_dsl::parse(&read_input(&file)?);
            if json {
                println!(
                    "{}",
                    serde_json::json!({ "pieces": p.board.pieces.len(), "errors": p.errors })
                );
            } else {
                for e in &p.errors {
                    println!("line {}: {}", e.line, e.message);
                }
                println!(
                    "{} pieces, {} links, {} steps{}, {} problem(s)",
                    p.board.pieces.len(),
                    p.board.links.len(),
                    p.board.steps.len(),
                    if p.board.sim.is_some() {
                        ", block field"
                    } else {
                        ""
                    },
                    p.errors.len()
                );
            }
            // The report above is the output; only the exit code says it failed.
            if p.errors.is_empty() {
                Ok(())
            } else {
                Err(Fail::new(1, "", ""))
            }
        }
        Cmd::Fmt { file } => {
            print!(
                "{}",
                blockies_dsl::format(&blockies_dsl::parse(&read_input(&file)?).board)
            );
            Ok(())
        }
        Cmd::Prompt => {
            println!("{}", blockies_dsl::AGENT_PROMPT);
            Ok(())
        }
        Cmd::Examples { name: Some(name) } => {
            print!("{}", example(&name)?);
            Ok(())
        }
        Cmd::Examples { name: None } => {
            for (name, _) in blockies_dsl::EXAMPLES {
                println!("{name}");
            }
            Ok(())
        }
    }
}

fn example(name: &str) -> Result<&'static str, Fail> {
    blockies_dsl::EXAMPLES
        .iter()
        .find(|(n, _)| *n == name)
        .map(|(_, d)| *d)
        .ok_or_else(|| {
            let names: Vec<_> = blockies_dsl::EXAMPLES.iter().map(|(n, _)| *n).collect();
            Fail::new(
                1,
                "no_example",
                format!("no example \"{name}\" (have {})", names.join(", ")),
            )
        })
}

fn print_reply(r: &OpReply, json: bool, ok: &str) {
    if json {
        println!("{}", serde_json::to_string(r).expect("serialise"));
        return;
    }
    println!("{ok} (rev {})", r.rev);
    for w in &r.warnings {
        println!("  line {}: {}", w.line, w.message);
    }
}

fn print_view(v: &BoardView) {
    let step = if v.steps == 0 {
        String::new()
    } else {
        format!("  step {}/{}  {}", v.step, v.steps, v.caption)
    };
    println!("{}  rev {}{step}", v.name, v.rev);
    let w = v.pieces.iter().map(|p| p.id.len()).max().unwrap_or(0);
    for p in &v.pieces {
        let driven = if p.driven { "  (follows a link)" } else { "" };
        println!(
            "  {:<w$}  {:<6}  {:<12}  {}{driven}",
            p.id,
            p.kind_name(),
            p.state,
            p.states.join("|")
        );
    }
    if let Some(s) = &v.sim {
        println!(
            "  field {}x{} {}  tick {}  hash {}",
            s.spec.width,
            s.spec.height,
            s.spec.scene.name(),
            s.tick,
            s.hash
        );
    }
}

trait KindName {
    fn kind_name(&self) -> &'static str;
}

impl KindName for blockies_protocol::PieceView {
    fn kind_name(&self) -> &'static str {
        self.kind.name()
    }
}

struct Api {
    base: String,
}

impl Api {
    fn ws_url(&self, board: &str) -> String {
        let rest = self
            .base
            .strip_prefix("https://")
            .map(|r| format!("wss://{r}"));
        let url = rest.unwrap_or_else(|| self.base.replacen("http://", "ws://", 1));
        format!("{url}/ws/{board}")
    }

    fn call<T: serde::de::DeserializeOwned>(
        &self,
        method: &str,
        path: &str,
        text: Option<String>,
        json: Option<String>,
    ) -> Result<T, Fail> {
        let agent = ureq::Agent::config_builder()
            .http_status_as_error(false)
            .build()
            .new_agent();
        let url = format!("{}{path}", self.base);
        let res = match (method, text, json) {
            ("GET", _, _) => agent.get(&url).call(),
            ("PUT", Some(body), _) => agent
                .put(&url)
                .header("content-type", "text/plain")
                .send(body),
            ("POST", _, Some(body)) => agent
                .post(&url)
                .header("content-type", "application/json")
                .send(body),
            _ => unreachable!("bad call"),
        };
        let mut res = res.map_err(|e| {
            Fail::new(
                3,
                "unreachable",
                format!("{}: {e} (is `blockies serve` running?)", self.base),
            )
        })?;
        let status = res.status().as_u16();
        let body = res
            .body_mut()
            .read_to_string()
            .map_err(|e| Fail::new(3, "io", e.to_string()))?;
        if status >= 400 {
            let e: ErrorBody = serde_json::from_str(&body).unwrap_or(ErrorBody {
                code: format!("http_{status}"),
                detail: body,
            });
            return Err(Fail::new(1, &e.code, e.detail));
        }
        serde_json::from_str(&body)
            .with_context(|| format!("bad reply from {url}"))
            .map_err(Fail::from)
    }

    fn op(&self, board: &str, op: &Op, json: bool) -> Out {
        let body = serde_json::to_string(op).expect("ops serialise");
        let reply: OpReply = self.call(
            "POST",
            &format!("/api/boards/{board}/ops"),
            None,
            Some(body),
        )?;
        print_reply(&reply, json, "ok");
        Ok(())
    }
}

fn serve(addr: &str, db: Option<PathBuf>, tick_ms: u64) -> Out {
    tracing_subscriber::fmt()
        .with_env_filter(
            tracing_subscriber::EnvFilter::try_from_default_env().unwrap_or_else(|_| "info".into()),
        )
        .init();
    let store: std::sync::Arc<dyn blockies_store::Store> = match &db {
        Some(path) => std::sync::Arc::new(
            blockies_store::SqliteStore::open(path)
                .map_err(|e| Fail::new(1, "store", e.to_string()))?,
        ),
        None => std::sync::Arc::new(
            blockies_store::SqliteStore::memory()
                .map_err(|e| Fail::new(1, "store", e.to_string()))?,
        ),
    };
    let rt = tokio::runtime::Runtime::new().map_err(|e| Fail::new(1, "runtime", e.to_string()))?;
    rt.block_on(async {
        let listener = tokio::net::TcpListener::bind(addr)
            .await
            .map_err(|e| Fail::new(1, "bind", format!("{addr}: {e}")))?;
        let hub = blockies_server::Hub::new(
            store,
            blockies_server::Config {
                tick: std::time::Duration::from_millis(tick_ms.max(1)),
            },
        );
        tracing::info!(
            "blockies serving on http://{addr} ({})",
            db.as_ref()
                .map_or_else(|| "in memory".to_string(), |p| p.display().to_string())
        );
        blockies_server::serve(listener, hub, async {
            let _ = tokio::signal::ctrl_c().await;
        })
        .await
        .map_err(|e| Fail::new(1, "serve", e.to_string()))
    })
}
