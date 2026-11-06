//! `cargo run --release -p blockies-tui --example render_bench`: cost of one frame.
use blockies_tui::{App, Source};
use ratatui::{Terminal, backend::TestBackend};
use std::time::{Duration, Instant};

fn main() {
    for (name, field) in [("web", false), ("sprint", false), ("sandbox", true)] {
        let dsl = blockies_dsl::EXAMPLES.iter().find(|(n, _)| *n == name).unwrap().1;
        let mut app = App::new(Source::Text { name: name.into(), dsl: dsl.into() }).unwrap();
        if field {
            app.on_key(ratatui::crossterm::event::KeyEvent::from(ratatui::crossterm::event::KeyCode::Tab));
        }
        for (w, h) in [(120u16, 40u16), (200, 60)] {
            let mut term = Terminal::new(TestBackend::new(w, h)).unwrap();
            let frames = 300;
            let t0 = Instant::now();
            for i in 0..frames {
                app.update(1.0 / 60.0, t0 + Duration::from_millis(16 * i));
                term.draw(|f| app.draw(f)).unwrap();
            }
            let us = t0.elapsed().as_secs_f64() * 1e6 / frames as f64;
            println!("{name:<8} {}  {w}x{h}  {us:>7.0} µs/frame (update + draw)", if field { "field" } else { "board" });
        }
    }
}
