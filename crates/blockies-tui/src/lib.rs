//! The terminal runtime. It animates a board toward whatever state the owner says (springs per
//! piece), and runs the block field as half-block pixels.
//!
//! - **Local**: the board lives in this process (`blockies tui board.blk`). Ops go straight to
//!   `blockies_ops::Doc`, the field ticks here.
//! - **Live**: connected to `blockies serve`. Ops go to the server; patches and lockstep ticks
//!   come back, so every connected runtime (and any agent using the CLI) sees the same board.
//!
//! The runtime degrades gracefully: whatever a terminal cannot show, it skips.

mod net;
pub mod view;

use anyhow::Context as _;
use blockies_anim::Spring;
use blockies_model::{Kind, Material};
use blockies_ops::{Doc, Op};
use blockies_protocol::{ClientMsg, HASH_EVERY, ServerMsg, hash_hex};
use blockies_sim::World;
use net::{Net, NetEvent};
use ratatui::{
    DefaultTerminal, Frame,
    crossterm::{
        event::{
            self, DisableMouseCapture, EnableMouseCapture, Event, KeyCode, KeyEvent, KeyEventKind,
            KeyModifiers, MouseButton, MouseEvent, MouseEventKind,
        },
        execute,
    },
    layout::{Constraint, Layout, Rect},
    style::{Modifier, Style},
    text::{Line, Span},
    widgets::{Block, Clear, Paragraph},
};
use std::path::PathBuf;
use std::time::{Duration, Instant};
use view::{BoardFrame, Canvas, FieldView, rgb};

/// Where the board comes from.
#[derive(Clone, Debug)]
pub enum Source {
    /// A DSL file, run locally. `r` reloads it.
    File(PathBuf),
    /// DSL text, run locally (examples, tests).
    Text { name: String, dsl: String },
    /// A server board: `ws://host:port/ws/<board>`.
    Live { url: String },
}

const FRAME: Duration = Duration::from_millis(16);
const SIM_TICK: Duration = Duration::from_millis(33);
const PLAY_EVERY: Duration = Duration::from_millis(2600);

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Mode {
    Board,
    Field,
}

enum Link {
    Local { path: Option<PathBuf> },
    Live { net: Net, online: bool, you: u32 },
}

pub struct App {
    name: String,
    doc: Doc,
    world: Option<World>,
    link: Link,
    mode: Mode,
    springs: Vec<Spring>,
    phase: Vec<f32>,
    selected: Option<usize>,
    hits: Vec<(Rect, usize)>,
    field: Option<FieldView>,
    field_area: Rect,
    material: Material,
    brush: u16,
    paused: bool,
    playing: Option<Instant>,
    help: bool,
    message: Option<(String, Instant)>,
    next_id: u64,
    sim_due: Instant,
    fps: f32,
    quit: bool,
}

impl std::fmt::Debug for App {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("App")
            .field("name", &self.name)
            .field("mode", &self.mode)
            .finish_non_exhaustive()
    }
}

/// Runs the TUI until the user quits.
pub fn run(source: Source) -> anyhow::Result<()> {
    let mut app = App::new(source)?;
    let mut terminal = ratatui::init();
    let _ = execute!(std::io::stdout(), EnableMouseCapture);
    let res = app.event_loop(&mut terminal);
    let _ = execute!(std::io::stdout(), DisableMouseCapture);
    ratatui::restore();
    res
}

impl App {
    pub fn new(source: Source) -> anyhow::Result<Self> {
        let (name, doc, link, note) = match source {
            Source::File(path) => {
                let text = std::fs::read_to_string(&path)
                    .with_context(|| format!("reading {}", path.display()))?;
                let (doc, errs) = Doc::from_dsl(&text);
                let name = path
                    .file_stem()
                    .map_or_else(|| "board".into(), |s| s.to_string_lossy().into_owned());
                (name, doc, Link::Local { path: Some(path) }, dsl_note(&errs))
            }
            Source::Text { name, dsl } => {
                let (doc, errs) = Doc::from_dsl(&dsl);
                (name, doc, Link::Local { path: None }, dsl_note(&errs))
            }
            Source::Live { url } => {
                let name = url.rsplit('/').next().unwrap_or("board").to_string();
                let net = Net::connect(url.clone());
                (
                    name,
                    Doc::new(Default::default()),
                    Link::Live {
                        net,
                        online: false,
                        you: 0,
                    },
                    Some(format!("connecting to {url}")),
                )
            }
        };
        let mut app = Self {
            name,
            world: None,
            doc,
            link,
            mode: Mode::Board,
            springs: Vec::new(),
            phase: Vec::new(),
            selected: None,
            hits: Vec::new(),
            field: None,
            field_area: Rect::default(),
            material: Material::Sand,
            brush: 3,
            paused: false,
            playing: None,
            help: false,
            message: None,
            next_id: 1,
            sim_due: Instant::now(),
            fps: 0.0,
            quit: false,
        };
        if matches!(app.link, Link::Local { .. }) {
            app.world = app.doc.board().sim.and_then(|s| World::new(s).ok());
        }
        app.reset_springs();
        if let Some(n) = note {
            app.say(n);
        }
        Ok(app)
    }

    pub const fn mode(&self) -> Mode {
        self.mode
    }

    pub const fn doc(&self) -> &Doc {
        &self.doc
    }

    pub const fn world(&self) -> Option<&World> {
        self.world.as_ref()
    }

    fn say(&mut self, msg: impl Into<String>) {
        self.message = Some((msg.into(), Instant::now()));
    }

    fn reset_springs(&mut self) {
        self.springs = self
            .doc
            .states()
            .iter()
            .map(|&s| Spring::at(s as f32))
            .collect();
        self.phase = vec![0.0; self.springs.len()];
        self.selected = self
            .selected
            .filter(|&i| i < self.springs.len())
            .or_else(|| self.first_selectable());
        if self.world.is_none() {
            self.mode = Mode::Board;
        }
    }

    fn retarget(&mut self) {
        if self.springs.len() != self.doc.states().len() {
            return self.reset_springs();
        }
        for (s, &v) in self.springs.iter_mut().zip(self.doc.states()) {
            s.target = v as f32;
        }
    }

    fn selectable(&self, i: usize) -> bool {
        let p = &self.doc.board().pieces[i];
        p.kind != Kind::Note && p.states.len() > 1 && !self.doc.is_driven(i)
    }

    fn first_selectable(&self) -> Option<usize> {
        (0..self.doc.board().pieces.len()).find(|&i| self.selectable(i))
    }

    fn select(&mut self, delta: isize) {
        let n = self.doc.board().pieces.len() as isize;
        if n == 0 {
            return;
        }
        let mut i = self.selected.map_or(-1, |s| s as isize);
        for _ in 0..n {
            i = (i + delta).rem_euclid(n);
            if self.selectable(i as usize) {
                self.selected = Some(i as usize);
                return;
            }
        }
    }

    /// Sends an op to whoever owns the board.
    pub fn act(&mut self, op: Op) {
        match &self.link {
            Link::Local { .. } => match self.doc.apply(&op) {
                Ok(applied) => {
                    if let (Some(p), Some(w)) = (applied.paint, &mut self.world) {
                        w.paint(p.x, p.y, p.r, p.material);
                    }
                    if !applied.warnings.is_empty() {
                        self.say(dsl_note(&applied.warnings).unwrap_or_default());
                    }
                    if matches!(op, Op::Load { .. }) {
                        self.world = self.doc.board().sim.and_then(|s| World::new(s).ok());
                        self.field = None;
                    }
                    self.retarget();
                }
                Err(e) => self.say(e.to_string()),
            },
            Link::Live { net, online, .. } => {
                if !online {
                    return self.say("offline: change not sent");
                }
                net.send(ClientMsg::Op {
                    id: self.next_id,
                    op,
                });
                self.next_id += 1;
            }
        }
    }

    fn shift_state(&mut self, delta: isize) {
        let Some(i) = self.selected else { return };
        let p = &self.doc.board().pieces[i];
        let n = p.states.len() as isize;
        let k = (self.doc.states()[i] as isize + delta).clamp(0, n - 1) as usize;
        let op = Op::Set {
            piece: p.id.clone(),
            state: p.states[k].clone(),
        };
        self.act(op);
    }

    fn step(&mut self, to: &str) {
        self.act(Op::Step { to: to.into() });
    }

    fn handle_net(&mut self) {
        let Link::Live { net, .. } = &self.link else {
            return;
        };
        let mut events = Vec::new();
        while let Some(e) = net.poll() {
            events.push(e);
        }
        for e in events {
            match e {
                NetEvent::Connected => {
                    self.set_online(true);
                    self.say("connected");
                }
                NetEvent::Closed(why) => {
                    self.set_online(false);
                    self.say(format!("offline ({why}); retrying"));
                }
                NetEvent::Msg(m) => self.on_server(*m),
            }
        }
    }

    fn set_online(&mut self, on: bool) {
        if let Link::Live { online, .. } = &mut self.link {
            *online = on;
        }
    }

    fn send_net(&self, msg: ClientMsg) {
        if let Link::Live { net, .. } = &self.link {
            net.send(msg);
        }
    }

    pub fn on_server(&mut self, m: ServerMsg) {
        match m {
            ServerMsg::Welcome {
                board,
                you,
                rev,
                dsl,
                states,
                step,
                sim,
                ..
            } => {
                self.name = board;
                if let Link::Live { you: me, .. } = &mut self.link {
                    *me = you;
                }
                self.doc = Doc::restore(&dsl, &states, step, rev);
                self.world = sim.and_then(|s| s.world().ok());
                self.field = None;
                self.reset_springs();
            }
            ServerMsg::Patch { rev, patches, .. } => {
                for p in &patches {
                    self.doc.apply_patch(p, rev);
                }
                if patches
                    .iter()
                    .any(|p| matches!(p, blockies_ops::Patch::Board { .. }))
                {
                    self.reset_springs();
                } else {
                    self.retarget();
                }
            }
            ServerMsg::Tick { tick, paints } => {
                let Some(w) = &mut self.world else { return };
                if w.tick() != tick {
                    // Missed or early tick: ask for the owner's snapshot instead of diverging.
                    self.send_net(ClientMsg::Resync);
                    return;
                }
                for p in &paints {
                    w.paint(p.x, p.y, p.r, p.material);
                }
                w.step();
                if w.tick().is_multiple_of(HASH_EVERY) {
                    let msg = ClientMsg::Hash {
                        tick: w.tick(),
                        hash: hash_hex(w.hash()),
                    };
                    self.send_net(msg);
                }
            }
            ServerMsg::Snapshot { sim } => {
                self.world = sim.world().ok();
            }
            ServerMsg::Ack { warnings, .. } => {
                if let Some(n) = dsl_note(&warnings) {
                    self.say(n);
                }
            }
            ServerMsg::Reject { code, detail, .. } => self.say(format!("{code}: {detail}")),
        }
    }

    /// Advances animation and the local field by `dt`. Returns true if anything moved.
    pub fn update(&mut self, dt: f32, now: Instant) -> bool {
        let mut moving = false;
        for s in &mut self.springs {
            moving |= s.step(dt);
        }
        let b = self.doc.board();
        for (i, p) in b.pieces.iter().enumerate() {
            if p.kind == Kind::Arrow {
                let n = p.last_state().max(1) as f32;
                let s = (self.springs[i].pos / n).clamp(0.0, 1.0);
                if s > 0.0 {
                    self.phase[i] = (self.phase[i] + dt * (0.15 + 0.9 * s)).fract();
                    moving = true;
                }
            }
        }
        if matches!(self.link, Link::Local { .. })
            && !self.paused
            && let Some(w) = &mut self.world
        {
            let mut ticks = 0;
            while self.sim_due <= now && ticks < 4 {
                w.step();
                self.sim_due += SIM_TICK;
                ticks += 1;
            }
            if self.sim_due < now {
                self.sim_due = now; // fell behind: skip instead of spiralling
            }
            moving |= ticks > 0;
        }
        if let Some(at) = self.playing
            && now >= at
        {
            let last = self.doc.board().steps.len();
            if self.doc.step().map_or(0, |k| k + 1) >= last {
                self.playing = None;
            } else {
                self.step("next");
                self.playing = Some(now + PLAY_EVERY);
            }
        }
        moving || self.world.is_some()
    }

    fn event_loop(&mut self, terminal: &mut DefaultTerminal) -> anyhow::Result<()> {
        let mut last = Instant::now();
        let mut dirty = true;
        let mut frames = 0u32;
        let mut fps_at = Instant::now();
        while !self.quit {
            let now = Instant::now();
            let dt = (now - last).as_secs_f32().min(0.1);
            last = now;
            self.handle_net();
            let moving = self.update(dt, now);
            if moving || dirty || self.message.is_some() {
                terminal.draw(|f| self.draw(f))?;
                frames += 1;
                dirty = false;
            }
            if fps_at.elapsed() >= Duration::from_secs(1) {
                self.fps = frames as f32 / fps_at.elapsed().as_secs_f32();
                frames = 0;
                fps_at = Instant::now();
            }
            if self
                .message
                .as_ref()
                .is_some_and(|(_, at)| at.elapsed() > Duration::from_secs(4))
            {
                self.message = None;
                dirty = true;
            }
            let wait = if moving {
                FRAME.saturating_sub(now.elapsed())
            } else {
                Duration::from_millis(100)
            };
            if event::poll(wait)? {
                // Drain everything pending so fast mouse drags do not lag behind.
                loop {
                    match event::read()? {
                        Event::Key(k) if k.kind != KeyEventKind::Release => self.on_key(k),
                        Event::Mouse(m) => self.on_mouse(m),
                        _ => {}
                    }
                    if !event::poll(Duration::ZERO)? {
                        break;
                    }
                }
                dirty = true;
            }
        }
        Ok(())
    }

    pub fn on_key(&mut self, k: KeyEvent) {
        if self.help {
            self.help = false;
            return;
        }
        let ctrl = k.modifiers.contains(KeyModifiers::CONTROL);
        match k.code {
            KeyCode::Char('q') | KeyCode::Esc => self.quit = true,
            KeyCode::Char('c') if ctrl => self.quit = true,
            KeyCode::Char('?') => self.help = true,
            KeyCode::Tab if self.world.is_some() => {
                self.mode = if self.mode == Mode::Board {
                    Mode::Field
                } else {
                    Mode::Board
                };
            }
            KeyCode::Char('r') => self.reload(),
            _ if self.mode == Mode::Board => self.board_key(k),
            _ => self.field_key(k),
        }
    }

    fn board_key(&mut self, k: KeyEvent) {
        match k.code {
            KeyCode::Down | KeyCode::Char('j') => self.select(1),
            KeyCode::Up | KeyCode::Char('k') => self.select(-1),
            KeyCode::Right | KeyCode::Char('l') => self.shift_state(1),
            KeyCode::Left | KeyCode::Char('h') => self.shift_state(-1),
            KeyCode::Enter | KeyCode::Char(' ') => {
                if let Some(i) = self.selected {
                    let piece = self.doc.board().pieces[i].id.clone();
                    self.act(Op::Cycle { piece });
                }
            }
            KeyCode::Char('n') => self.step("next"),
            KeyCode::Char('p') => self.step("prev"),
            KeyCode::Char('0') => self.step("start"),
            KeyCode::Char('P') => {
                self.playing = if self.playing.is_some() {
                    None
                } else {
                    Some(Instant::now())
                };
            }
            _ => {}
        }
    }

    fn field_key(&mut self, k: KeyEvent) {
        let Some(w) = &self.world else { return };
        let mut v = self
            .field
            .unwrap_or_else(|| FieldView::fit(w, self.field_area));
        let step = 8 * v.zoom as i32;
        match k.code {
            KeyCode::Left | KeyCode::Char('h') => v.ox -= step,
            KeyCode::Right | KeyCode::Char('l') => v.ox += step,
            KeyCode::Up | KeyCode::Char('k') => v.oy -= step,
            KeyCode::Down | KeyCode::Char('j') => v.oy += step,
            KeyCode::Char('+' | '=') if v.zoom > 1 => {
                v.ox += i32::from(self.field_area.width) * v.zoom as i32 / 4;
                v.oy += i32::from(self.field_area.height) * v.zoom as i32 / 2;
                v.zoom /= 2;
            }
            KeyCode::Char('-') if v.zoom < 64 => {
                v.zoom *= 2;
                v.ox -= i32::from(self.field_area.width) * v.zoom as i32 / 4;
                v.oy -= i32::from(self.field_area.height) * v.zoom as i32 / 2;
            }
            KeyCode::Char('f') => v = FieldView::fit(w, self.field_area),
            KeyCode::Char('1') => self.material = Material::Sand,
            KeyCode::Char('2') => self.material = Material::Water,
            KeyCode::Char('3') => self.material = Material::Wall,
            KeyCode::Char('4') => self.material = Material::Empty,
            KeyCode::Char('[') => self.brush = self.brush.saturating_sub(1).max(1),
            KeyCode::Char(']') => self.brush = (self.brush + 1).min(64),
            KeyCode::Char(' ') if matches!(self.link, Link::Local { .. }) => {
                self.paused = !self.paused
            }
            _ => {}
        }
        self.field = Some(v);
    }

    pub fn on_mouse(&mut self, m: MouseEvent) {
        let down = matches!(
            m.kind,
            MouseEventKind::Down(MouseButton::Left) | MouseEventKind::Drag(MouseButton::Left)
        );
        match self.mode {
            Mode::Board if matches!(m.kind, MouseEventKind::Down(MouseButton::Left)) => {
                let hit = self
                    .hits
                    .iter()
                    .rev()
                    .find(|(r, _)| r.contains((m.column, m.row).into()))
                    .map(|&(_, i)| i);
                if let Some(i) = hit.filter(|&i| self.selectable(i)) {
                    self.selected = Some(i);
                    let piece = self.doc.board().pieces[i].id.clone();
                    self.act(Op::Cycle { piece });
                }
            }
            Mode::Field if down && self.field_area.contains((m.column, m.row).into()) => {
                if let (Some(v), Some(_)) = (self.field, &self.world) {
                    let (x, y) = v.at(self.field_area, m.column, m.row);
                    let r = self.brush * v.zoom as u16;
                    self.act(Op::Paint {
                        x,
                        y,
                        r,
                        material: self.material,
                    });
                }
            }
            _ => {}
        }
    }

    fn reload(&mut self) {
        let Link::Local { path: Some(path) } = &self.link else {
            return;
        };
        match std::fs::read_to_string(path) {
            Ok(dsl) => {
                self.act(Op::Load { dsl });
                self.say("reloaded");
            }
            Err(e) => self.say(format!("reload failed: {e}")),
        }
    }

    pub fn draw(&mut self, f: &mut Frame<'_>) {
        let [main, status, keys] = Layout::vertical([
            Constraint::Min(3),
            Constraint::Length(1),
            Constraint::Length(1),
        ])
        .areas(f.area());
        match (self.mode, &self.world) {
            (Mode::Field, Some(w)) => {
                self.field_area = main;
                let v = *self.field.get_or_insert_with(|| FieldView::fit(w, main));
                view::field(
                    &mut Canvas {
                        buf: f.buffer_mut(),
                        area: main,
                    },
                    w,
                    v,
                );
            }
            _ => {
                self.field_area = main;
                let t: Vec<f32> = self.springs.iter().map(|s| s.pos).collect();
                let frame = BoardFrame {
                    doc: &self.doc,
                    t: &t,
                    phase: &self.phase,
                    selected: self.selected,
                };
                self.hits = view::board(
                    &mut Canvas {
                        buf: f.buffer_mut(),
                        area: main,
                    },
                    &frame,
                );
            }
        }
        f.render_widget(Paragraph::new(self.status_line()), status);
        f.render_widget(
            Paragraph::new(self.keys_line()).style(Style::default().fg(rgb((110, 114, 128)))),
            keys,
        );
        if self.help {
            help(f);
        }
    }

    fn status_line(&self) -> Line<'static> {
        let accent = Style::default()
            .fg(rgb((110, 150, 255)))
            .add_modifier(Modifier::BOLD);
        let dim = Style::default().fg(rgb((128, 132, 145)));
        let mut spans = vec![
            Span::styled(" blockies ", accent),
            Span::styled(
                format!("{} ", self.name),
                Style::default().add_modifier(Modifier::BOLD),
            ),
        ];
        let link = match &self.link {
            Link::Local { .. } => Span::styled("local ", dim),
            Link::Live { online: true, .. } => {
                Span::styled("● live ", Style::default().fg(rgb((84, 200, 120))))
            }
            Link::Live { online: false, .. } => {
                Span::styled("○ offline ", Style::default().fg(rgb((232, 84, 84))))
            }
        };
        spans.push(link);
        spans.push(Span::styled(format!("rev {} ", self.doc.rev()), dim));
        match (self.mode, &self.world) {
            (Mode::Field, Some(w)) => {
                let (fw, fh) = w.size();
                let zoom = self.field.map_or(1, |v| v.zoom);
                spans.push(Span::styled(
                    format!(
                        "field {fw}×{fh} tick {} awake {}% zoom 1:{zoom} {} r{} {}",
                        w.tick(),
                        100 * w.awake_count() / w.tiles().max(1),
                        self.material.name(),
                        self.brush,
                        if self.paused { "paused " } else { "" }
                    ),
                    dim,
                ));
            }
            _ => {
                let n = self.doc.board().steps.len();
                if n > 0 {
                    let k = self.doc.step().map_or(0, |k| k + 1);
                    spans.push(Span::styled(format!("step {k}/{n} "), dim));
                    let cap = if k == 0 {
                        "start: press n to walk through"
                    } else {
                        self.doc.caption()
                    };
                    spans.push(Span::styled(
                        cap.to_string(),
                        Style::default().add_modifier(Modifier::ITALIC),
                    ));
                }
            }
        }
        if let Some((m, _)) = &self.message {
            spans.push(Span::styled(
                format!("  {m}"),
                Style::default().fg(rgb((232, 170, 60))),
            ));
        }
        spans.push(Span::styled(format!("  {:.0} fps", self.fps), dim));
        Line::from(spans)
    }

    fn keys_line(&self) -> String {
        let tab = if self.world.is_some() {
            "  tab field/board"
        } else {
            ""
        };
        match self.mode {
            Mode::Board => format!(
                " j/k select  h/l state  enter cycle  n/p step  P play  ? help  q quit{tab}"
            ),
            Mode::Field => format!(
                " drag paint  1 sand 2 water 3 wall 4 erase  [ ] brush  arrows pan  +/- zoom  f fit{tab}  q quit"
            ),
        }
    }
}

fn dsl_note(errs: &[blockies_dsl::LineError]) -> Option<String> {
    let first = errs.first()?;
    Some(format!(
        "{} DSL problem(s); line {}: {}",
        errs.len(),
        first.line,
        first.message
    ))
}

fn help(f: &mut Frame<'_>) {
    let text = "\
Board
  j / k, ↑ / ↓     select a piece
  h / l, ← / →     previous / next state
  enter, space     cycle state (or click a piece)
  n / p / 0        next / previous step / start
  P                play the steps like slides

Block field (tab)
  mouse drag       paint
  1 2 3 4          sand, water, wall, erase
  [ ]              brush size
  arrows, + / -    pan, zoom    f  fit
  space            pause (local)

  r reload file    q quit       any key closes this";
    let area = f.area();
    let (w, h) = (52.min(area.width), 20.min(area.height));
    let r = Rect::new(
        area.x + (area.width - w) / 2,
        area.y + (area.height - h) / 2,
        w,
        h,
    );
    f.render_widget(Clear, r);
    f.render_widget(
        Paragraph::new(text).block(Block::bordered().title(" keys ")),
        r,
    );
}

#[cfg(test)]
mod tests {
    use super::*;
    use ratatui::{Terminal, backend::TestBackend};

    fn app(name: &str) -> App {
        let dsl = blockies_dsl::EXAMPLES
            .iter()
            .find(|(n, _)| *n == name)
            .unwrap()
            .1;
        App::new(Source::Text {
            name: name.into(),
            dsl: dsl.into(),
        })
        .unwrap()
    }

    fn render(app: &mut App, w: u16, h: u16) -> String {
        let mut t = Terminal::new(TestBackend::new(w, h)).unwrap();
        t.draw(|f| app.draw(f)).unwrap();
        let buf = t.backend().buffer();
        (0..h)
            .map(|y| (0..w).map(|x| buf[(x, y)].symbol()).collect::<String>() + "\n")
            .collect()
    }

    fn key(c: KeyCode) -> KeyEvent {
        KeyEvent::new(c, KeyModifiers::NONE)
    }

    #[test]
    fn draws_every_piece_of_the_web_board() {
        let mut a = app("web");
        let s = render(&mut a, 150, 42);
        for label in [
            "Browser",
            "CDN edge",
            "API server",
            "Postgres",
            "GET /home",
            "Cache hit",
            "Traffic",
            "API CPU",
        ] {
            assert!(s.contains(label), "missing {label}:\n{s}");
        }
        assert!(s.contains("step 0/6"), "{s}");
    }

    #[test]
    fn keys_drive_states_and_steps_and_springs_follow() {
        let mut a = app("web");
        a.on_key(key(KeyCode::Char('n')));
        assert_eq!(a.doc().step(), Some(0));
        let browser = a.doc().board().index("browser").unwrap();
        assert_eq!(a.doc().state_name(browser), "waiting");
        assert!(a.springs[browser].pos < 1.0, "spring starts where it was");
        let t0 = Instant::now();
        for i in 0..90 {
            a.update(1.0 / 60.0, t0 + Duration::from_millis(16 * i));
        }
        assert!(
            (a.springs[browser].pos - 1.0).abs() < 1e-3,
            "spring settled: {}",
            a.springs[browser].pos
        );

        // Select the traffic slider and push it to "spike": its links follow.
        a.selected = a.doc().board().index("load");
        a.on_key(key(KeyCode::Char('l')));
        a.on_key(key(KeyCode::Char('l')));
        let api = a.doc().board().index("api").unwrap();
        assert_eq!(a.doc().state_name(api), "overloaded");
    }

    #[test]
    fn field_view_paints_and_ticks() {
        let mut a = app("sandbox");
        assert!(a.world().is_some());
        a.on_key(key(KeyCode::Tab));
        assert_eq!(a.mode(), Mode::Field);
        let s = render(&mut a, 120, 40);
        assert!(s.contains('▀'), "{s}");
        let before = a.world().unwrap().hash();
        let area = a.field_area;
        let (column, row) = (area.x + area.width / 2, area.y + area.height / 2);
        a.on_mouse(MouseEvent {
            kind: MouseEventKind::Down(MouseButton::Left),
            column,
            row,
            modifiers: KeyModifiers::NONE,
        });
        assert_ne!(a.world().unwrap().hash(), before, "paint landed");
        let t = a.world().unwrap().tick();
        a.update(0.1, Instant::now() + Duration::from_millis(200));
        assert!(a.world().unwrap().tick() > t, "field ticks locally");
    }

    #[test]
    fn server_messages_drive_a_live_mirror() {
        let mut a = app("web");
        let (owner, _) = Doc::from_dsl(blockies_dsl::EXAMPLES[2].1);
        a.on_server(ServerMsg::Welcome {
            protocol: 1,
            board: "sprint".into(),
            you: 3,
            rev: 4,
            dsl: owner.dsl(),
            states: owner.states().to_vec(),
            step: None,
            sim: None,
        });
        assert_eq!(a.doc().board().pieces.len(), owner.board().pieces.len());
        a.on_server(ServerMsg::Patch {
            rev: 5,
            by: 1,
            patches: vec![blockies_ops::Patch::State {
                piece: "t1".into(),
                state: 2,
            }],
        });
        assert_eq!(a.doc().states()[a.doc().board().index("t1").unwrap()], 2);
        a.on_server(ServerMsg::Reject {
            id: 1,
            code: "driven".into(),
            detail: "x".into(),
        });
        assert!(a.message.as_ref().unwrap().0.starts_with("driven"));
    }

    #[test]
    fn tiny_terminals_do_not_panic() {
        for name in ["web", "water", "sprint", "sandbox"] {
            let mut a = app(name);
            for (w, h) in [(1, 1), (10, 4), (40, 12)] {
                render(&mut a, w, h);
                a.on_key(key(KeyCode::Tab));
                render(&mut a, w, h);
                a.on_key(key(KeyCode::Tab));
            }
        }
    }
}
