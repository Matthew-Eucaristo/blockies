//! The agent loop for real: start `blockies serve`, drive it with `blockies ... --json`.

use std::net::TcpListener;
use std::process::{Child, Command, Output, Stdio};
use std::time::{Duration, Instant};

const BIN: &str = env!("CARGO_BIN_EXE_blockies");

struct Server(Child);

impl Drop for Server {
    fn drop(&mut self) {
        let _ = self.0.kill();
        let _ = self.0.wait();
    }
}

fn start() -> (Server, String) {
    let port = TcpListener::bind("127.0.0.1:0")
        .unwrap()
        .local_addr()
        .unwrap()
        .port();
    let addr = format!("127.0.0.1:{port}");
    let child = Command::new(BIN)
        .args(["serve", "--memory", "--addr", &addr])
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .spawn()
        .unwrap();
    let deadline = Instant::now() + Duration::from_secs(10);
    while std::net::TcpStream::connect(&addr).is_err() {
        assert!(Instant::now() < deadline, "server did not start");
        std::thread::sleep(Duration::from_millis(20));
    }
    (Server(child), format!("http://{addr}"))
}

fn blockies(server: &str, args: &[&str]) -> (i32, serde_json::Value, Output) {
    let out = Command::new(BIN)
        .args(["--server", server, "--json"])
        .args(args)
        .output()
        .unwrap();
    let v = serde_json::from_slice(&out.stdout).unwrap_or(serde_json::Value::Null);
    (out.status.code().unwrap(), v, out)
}

#[test]
fn agent_drives_a_board_from_the_shell() {
    let (_server, url) = start();
    let web = concat!(
        env!("CARGO_MANIFEST_DIR"),
        "/../blockies-dsl/examples/web.blk"
    );

    let (code, v, out) = blockies(&url, &["new", "web", web]);
    assert_eq!(code, 0, "{out:?}");
    assert_eq!(v["rev"], 1);

    // A step sets every independent piece to the story's state at that step...
    let (code, _, _) = blockies(&url, &["step", "web", "2"]);
    assert_eq!(code, 0);
    // ...then free play on top of it.
    let (code, _, _) = blockies(&url, &["move", "web", "req", "db"]);
    assert_eq!(code, 0);
    let (code, _, _) = blockies(&url, &["set", "web", "load", "spike"]);
    assert_eq!(code, 0);

    let (code, v, _) = blockies(&url, &["show", "web"]);
    assert_eq!(code, 0);
    let state = |id: &str| {
        v["pieces"]
            .as_array()
            .unwrap()
            .iter()
            .find(|p| p["id"] == id)
            .unwrap()["state"]
            .clone()
    };
    assert_eq!(state("api"), "overloaded", "link followed the slider");
    assert_eq!(state("req"), "3", "from the CDN (step 2) to the database");
    assert_eq!(v["step"], 2);

    // Refusals: exit 1 with a stable code the agent can act on.
    let (code, v, _) = blockies(&url, &["set", "web", "api", "idle"]);
    assert_eq!((code, v["error"]["code"].as_str()), (1, Some("driven")));
    let (code, v, _) = blockies(&url, &["paint", "web", "1", "1"]);
    assert_eq!((code, v["error"]["code"].as_str()), (1, Some("no_sim")));
    let (code, v, _) = blockies(&url, &["show", "nope"]);
    assert_eq!((code, v["error"]["code"].as_str()), (1, Some("no_board")));

    let (code, v, _) = blockies(&url, &["ls"]);
    assert_eq!(code, 0);
    assert_eq!(v[0]["name"], "web");
}

#[test]
fn offline_and_local_commands() {
    let (code, v, _) = blockies("http://127.0.0.1:9", &["show", "web"]);
    assert_eq!(
        (code, v["error"]["code"].as_str()),
        (3, Some("unreachable"))
    );

    let out = Command::new(BIN).arg("prompt").output().unwrap();
    assert!(String::from_utf8_lossy(&out.stdout).contains("box <id>"));

    let dir = tempfile::tempdir().unwrap();
    let bad = dir.path().join("bad.blk");
    std::fs::write(&bad, "box a states x|y\nblob q\n").unwrap();
    let (code, v, _) = blockies("http://unused", &["check", bad.to_str().unwrap()]);
    assert_eq!(code, 1);
    assert_eq!(v["errors"][0]["line"], 2);
    assert_eq!(v["pieces"], 1);
}
