//! Persistence behind one small trait, so a hosted Postgres (or anything else) can replace
//! SQLite without touching the server. Each board is a snapshot row plus an append-only op
//! log; replaying the log from an older snapshot reproduces the board.

use rusqlite::{Connection, OptionalExtension, params};
use serde::{Deserialize, Serialize};
use std::{path::Path, sync::Mutex};

#[derive(Debug, thiserror::Error)]
pub enum StoreError {
    #[error("sqlite: {0}")]
    Sqlite(#[from] rusqlite::Error),
    #[error("stored states are corrupt: {0}")]
    Corrupt(String),
}

pub type Result<T> = std::result::Result<T, StoreError>;

/// A board as stored: its DSL and where every piece is.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Saved {
    pub name: String,
    pub dsl: String,
    pub states: Vec<usize>,
    pub step: Option<usize>,
    pub rev: u64,
}

#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize)]
pub struct BoardInfo {
    pub name: String,
    pub rev: u64,
    pub updated: String,
}

pub trait Store: Send + Sync + 'static {
    fn load(&self, name: &str) -> Result<Option<Saved>>;
    /// Saves the snapshot and appends the op that produced it, atomically.
    fn commit(&self, saved: &Saved, op_json: Option<&str>) -> Result<()>;
    fn list(&self) -> Result<Vec<BoardInfo>>;
    /// Ops with `rev > after`, oldest first.
    fn ops_since(&self, name: &str, after: u64) -> Result<Vec<(u64, String)>>;
}

#[derive(Debug)]
pub struct SqliteStore {
    conn: Mutex<Connection>,
}

const SCHEMA: &str = "
PRAGMA journal_mode = WAL;
PRAGMA synchronous = NORMAL;
CREATE TABLE IF NOT EXISTS boards (
    name    TEXT PRIMARY KEY,
    dsl     TEXT NOT NULL,
    states  TEXT NOT NULL,
    step    INTEGER,
    rev     INTEGER NOT NULL,
    updated TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE TABLE IF NOT EXISTS ops (
    board TEXT NOT NULL,
    rev   INTEGER NOT NULL,
    op    TEXT NOT NULL,
    at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    PRIMARY KEY (board, rev)
);
";

impl SqliteStore {
    pub fn open(path: impl AsRef<Path>) -> Result<Self> {
        Self::init(Connection::open(path)?)
    }

    pub fn memory() -> Result<Self> {
        Self::init(Connection::open_in_memory()?)
    }

    fn init(conn: Connection) -> Result<Self> {
        conn.execute_batch(SCHEMA)?;
        Ok(Self {
            conn: Mutex::new(conn),
        })
    }

    fn conn(&self) -> std::sync::MutexGuard<'_, Connection> {
        // A panic while holding the lock cannot leave SQLite half-written (transactions roll back).
        self.conn
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner)
    }
}

fn encode_states(s: &[usize]) -> String {
    s.iter()
        .map(ToString::to_string)
        .collect::<Vec<_>>()
        .join(",")
}

fn decode_states(s: &str) -> Result<Vec<usize>> {
    if s.is_empty() {
        return Ok(Vec::new());
    }
    s.split(',')
        .map(|v| v.parse().map_err(|_| StoreError::Corrupt(s.to_string())))
        .collect()
}

impl Store for SqliteStore {
    fn load(&self, name: &str) -> Result<Option<Saved>> {
        let row = self
            .conn()
            .query_row(
                "SELECT dsl, states, step, rev FROM boards WHERE name = ?1",
                [name],
                |r| {
                    Ok((
                        r.get::<_, String>(0)?,
                        r.get::<_, String>(1)?,
                        r.get::<_, Option<i64>>(2)?,
                        r.get::<_, i64>(3)?,
                    ))
                },
            )
            .optional()?;
        row.map(|(dsl, states, step, rev)| {
            Ok(Saved {
                name: name.to_string(),
                dsl,
                states: decode_states(&states)?,
                step: step.map(|s| s as usize),
                rev: rev as u64,
            })
        })
        .transpose()
    }

    fn commit(&self, s: &Saved, op_json: Option<&str>) -> Result<()> {
        let mut conn = self.conn();
        let tx = conn.transaction()?;
        tx.execute(
            "INSERT INTO boards (name, dsl, states, step, rev) VALUES (?1, ?2, ?3, ?4, ?5)
             ON CONFLICT(name) DO UPDATE SET dsl = ?2, states = ?3, step = ?4, rev = ?5,
             updated = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')",
            params![
                s.name,
                s.dsl,
                encode_states(&s.states),
                s.step.map(|v| v as i64),
                s.rev as i64
            ],
        )?;
        if let Some(op) = op_json {
            tx.execute(
                "INSERT OR REPLACE INTO ops (board, rev, op) VALUES (?1, ?2, ?3)",
                params![s.name, s.rev as i64, op],
            )?;
        }
        tx.commit()?;
        Ok(())
    }

    fn list(&self) -> Result<Vec<BoardInfo>> {
        let conn = self.conn();
        let mut st = conn.prepare("SELECT name, rev, updated FROM boards ORDER BY name")?;
        let rows = st.query_map([], |r| {
            Ok(BoardInfo {
                name: r.get(0)?,
                rev: r.get::<_, i64>(1)? as u64,
                updated: r.get(2)?,
            })
        })?;
        Ok(rows.collect::<std::result::Result<_, _>>()?)
    }

    fn ops_since(&self, name: &str, after: u64) -> Result<Vec<(u64, String)>> {
        let conn = self.conn();
        let mut st =
            conn.prepare("SELECT rev, op FROM ops WHERE board = ?1 AND rev > ?2 ORDER BY rev")?;
        let rows = st.query_map(params![name, after as i64], |r| {
            Ok((r.get::<_, i64>(0)? as u64, r.get(1)?))
        })?;
        Ok(rows.collect::<std::result::Result<_, _>>()?)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn saved(rev: u64) -> Saved {
        Saved {
            name: "demo".into(),
            dsl: "box a states x|y".into(),
            states: vec![1],
            step: Some(0),
            rev,
        }
    }

    #[test]
    fn commit_load_list_and_log() {
        let s = SqliteStore::memory().unwrap();
        assert_eq!(s.load("demo").unwrap(), None);
        s.commit(&saved(1), Some(r#"{"op":"cycle","piece":"a"}"#))
            .unwrap();
        s.commit(&saved(2), Some(r#"{"op":"cycle","piece":"a"}"#))
            .unwrap();
        assert_eq!(s.load("demo").unwrap(), Some(saved(2)));
        assert_eq!(s.list().unwrap()[0].rev, 2);
        assert_eq!(s.ops_since("demo", 1).unwrap().len(), 1);
    }

    #[test]
    fn survives_reopen() {
        let dir = std::env::temp_dir().join(format!("blockies-store-{}", std::process::id()));
        std::fs::create_dir_all(&dir).unwrap();
        let path = dir.join("t.db");
        SqliteStore::open(&path)
            .unwrap()
            .commit(&saved(5), None)
            .unwrap();
        assert_eq!(
            SqliteStore::open(&path)
                .unwrap()
                .load("demo")
                .unwrap()
                .unwrap()
                .rev,
            5
        );
        std::fs::remove_dir_all(dir).unwrap();
    }
}
