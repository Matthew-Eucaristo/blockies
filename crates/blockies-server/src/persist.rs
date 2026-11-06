//! Writes go through one thread, so commits keep their order and board actors never block on
//! disk. Commits that queue up are written back to back.

use blockies_store::{Saved, Store};
use std::sync::{Arc, mpsc};

#[derive(Clone, Debug)]
pub struct Persister {
    tx: mpsc::Sender<(Saved, Option<String>)>,
}

impl Persister {
    pub fn start(store: Arc<dyn Store>) -> Self {
        let (tx, rx) = mpsc::channel::<(Saved, Option<String>)>();
        std::thread::Builder::new()
            .name("blockies-store".into())
            .spawn(move || {
                while let Ok(first) = rx.recv() {
                    let mut batch = vec![first];
                    batch.extend(rx.try_iter());
                    for (saved, op) in &batch {
                        if let Err(e) = store.commit(saved, op.as_deref()) {
                            tracing::error!(board = %saved.name, "store commit failed: {e}");
                        }
                    }
                }
            })
            .expect("spawn store thread");
        Self { tx }
    }

    pub fn commit(&self, saved: Saved, op: Option<String>) {
        if self.tx.send((saved, op)).is_err() {
            tracing::error!("store thread is gone; change not persisted");
        }
    }
}
