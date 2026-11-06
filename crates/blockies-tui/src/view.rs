//! Drawing. Boards are laid out in board coordinates (about 1400 × 900, piece centres) and
//! mapped onto the terminal, where a cell is roughly twice as tall as it is wide. The block
//! field is drawn as half-block pixels (`▀`, top pixel in the foreground colour, bottom pixel
//! in the background colour), two pixels per cell.

use blockies_model::{Kind, Piece, Tone, tone};
use blockies_ops::Doc;
use blockies_sim::{PALETTE, World};
use ratatui::{
    buffer::Buffer,
    layout::Rect,
    style::{Color, Modifier, Style},
};

pub type Rgb = (u8, u8, u8);

const MUTED: Rgb = (128, 132, 145);
const DIM: Rgb = (78, 82, 94);
const NOTE: Rgb = (232, 200, 120);
const FIELD_BG: Rgb = (18, 18, 22);

pub const fn rgb(c: Rgb) -> Color {
    Color::Rgb(c.0, c.1, c.2)
}

pub fn tone_rgb(t: Tone) -> Option<Rgb> {
    match t {
        Tone::Good => Some((84, 200, 120)),
        Tone::Busy => Some((232, 170, 60)),
        Tone::Bad => Some((232, 84, 84)),
        Tone::Neutral => None,
        Tone::Accent(0) => Some((110, 150, 255)),
        Tone::Accent(1) => Some((172, 124, 255)),
        Tone::Accent(2) => Some((60, 196, 200)),
        Tone::Accent(_) => Some((236, 112, 180)),
    }
}

fn mix(a: Rgb, b: Rgb, f: f32) -> Rgb {
    let l = |x: u8, y: u8| {
        (f32::from(x) + (f32::from(y) - f32::from(x)) * f.clamp(0.0, 1.0)).round() as u8
    };
    (l(a.0, b.0), l(a.1, b.1), l(a.2, b.2))
}

/// Colour of a piece at continuous state `t`, blending neighbouring states like the web board.
fn state_rgb(p: &Piece, t: f32) -> Rgb {
    let n = p.states.len().saturating_sub(1);
    let t = t.clamp(0.0, n as f32);
    let i = (t.floor() as usize).min(n.saturating_sub(1));
    let f = if n == 0 { 0.0 } else { t - i as f32 };
    let c = |k: usize| tone_rgb(tone(&p.states[k.min(n)], k)).unwrap_or(MUTED);
    mix(c(i), c(i + 1), f)
}

/// Writes into a buffer, clipped to an area. Coordinates are absolute and may be off-screen.
#[derive(Debug)]
pub struct Canvas<'a> {
    pub buf: &'a mut Buffer,
    pub area: Rect,
}

impl Canvas<'_> {
    fn inside(&self, x: i32, y: i32) -> bool {
        let a = self.area;
        x >= i32::from(a.x)
            && y >= i32::from(a.y)
            && x < i32::from(a.right())
            && y < i32::from(a.bottom())
    }

    pub fn ch(&mut self, x: i32, y: i32, c: char, style: Style) {
        if self.inside(x, y) {
            self.buf[(x as u16, y as u16)].set_char(c).set_style(style);
        }
    }

    pub fn text(&mut self, x: i32, y: i32, s: &str, style: Style) {
        for (i, c) in s.chars().enumerate() {
            self.ch(x + i as i32, y, c, style);
        }
    }

    pub fn centered(&mut self, cx: i32, y: i32, s: &str, style: Style) {
        let n = s.chars().count() as i32;
        self.text(cx - n / 2, y, s, style);
    }

    fn frame(&mut self, x0: i32, y0: i32, w: i32, h: i32, style: Style, double: bool) {
        let (tl, tr, bl, br, hz, vt) = if double {
            ('╔', '╗', '╚', '╝', '═', '║')
        } else {
            ('╭', '╮', '╰', '╯', '─', '│')
        };
        for x in x0 + 1..x0 + w - 1 {
            self.ch(x, y0, hz, style);
            self.ch(x, y0 + h - 1, hz, style);
        }
        for y in y0 + 1..y0 + h - 1 {
            self.ch(x0, y, vt, style);
            self.ch(x0 + w - 1, y, vt, style);
        }
        self.ch(x0, y0, tl, style);
        self.ch(x0 + w - 1, y0, tr, style);
        self.ch(x0, y0 + h - 1, bl, style);
        self.ch(x0 + w - 1, y0 + h - 1, br, style);
    }

    fn clear(&mut self, x0: i32, y0: i32, w: i32, h: i32) {
        for y in y0..y0 + h {
            for x in x0..x0 + w {
                self.ch(x, y, ' ', Style::default());
            }
        }
    }
}

/// Board units → terminal cells.
#[derive(Clone, Copy, Debug)]
struct Xf {
    /// Board units per column (rows are twice as tall).
    k: f32,
    ox: f32,
    oy: f32,
    ax: f32,
    ay: f32,
}

impl Xf {
    fn cell(&self, x: f32, y: f32) -> (i32, i32) {
        (
            (self.ax + (x - self.ox) / self.k).round() as i32,
            (self.ay + (y - self.oy) / (2.0 * self.k)).round() as i32,
        )
    }
}

/// Board-space size of a placed piece (same proportions as the web board).
fn world_size(p: &Piece) -> (f32, f32) {
    let text_w = |s: &str, px: f32| s.chars().count() as f32 * px * 0.56;
    let sz = p.size.map(|s| (s.x as f32, s.y as f32));
    match p.kind {
        Kind::Box => sz.unwrap_or(((text_w(&p.label, 14.0) + 44.0).max(140.0), 64.0)),
        Kind::Switch => (60.0, 34.0),
        Kind::Slider => (
            ((p.states.len().saturating_sub(1)) as f32 * 96.0).max(180.0) + 30.0,
            70.0,
        ),
        Kind::Bar => (90.0, 220.0),
        Kind::Card => sz.unwrap_or((210.0, 120.0)),
        Kind::Note => {
            let lines: Vec<_> = p.label.lines().collect();
            (
                lines.iter().map(|l| text_w(l, 13.0)).fold(0.0, f32::max) + 32.0,
                lines.len() as f32 * 19.0 + 26.0,
            )
        }
        Kind::Arrow | Kind::Token => (40.0, 40.0),
    }
}

fn centre(p: &Piece) -> (f32, f32) {
    p.at.map_or((0.0, 0.0), |a| (a.x as f32, a.y as f32))
}

/// Token position at continuous route index `t`: arcs between the tops of the route's boxes.
fn token_pos(doc: &Doc, p: &Piece, t: f32) -> (f32, f32) {
    let (dx, dy) = p.offset.map_or((0.0, 0.0), |o| (o.x as f32, o.y as f32));
    let pts: Vec<(f32, f32)> = p
        .route
        .iter()
        .filter_map(|id| doc.board().piece(id))
        .map(|q| {
            let (x, y) = centre(q);
            (x + dx, y - world_size(q).1 / 2.0 + dy)
        })
        .collect();
    if pts.len() < 2 {
        return pts.first().copied().unwrap_or_default();
    }
    let segs = pts.len() - 1;
    let t = t.clamp(0.0, segs as f32);
    let i = (t.floor() as usize).min(segs - 1);
    let f = t - i as f32;
    let (a, b) = (pts[i], pts[i + 1]);
    let d = ((b.0 - a.0).powi(2) + (b.1 - a.1).powi(2)).sqrt();
    let lift = if b.0 >= a.0 {
        0.16 * d + 22.0
    } else {
        0.32 * d + 44.0
    };
    let c = ((a.0 + b.0) / 2.0, a.1.min(b.1) - lift);
    let q =
        |a: f32, c: f32, b: f32| (1.0 - f) * (1.0 - f) * a + 2.0 * (1.0 - f) * f * c + f * f * b;
    (q(a.0, c.0, b.0), q(a.1, c.1, b.1))
}

/// Everything the board renderer needs from the app.
#[derive(Debug)]
pub struct BoardFrame<'a> {
    pub doc: &'a Doc,
    /// Continuous state of every piece (springs).
    pub t: &'a [f32],
    /// Arrow flow phase in `0..1`.
    pub phase: &'a [f32],
    pub selected: Option<usize>,
}

/// Draws the board and returns each piece's clickable cell rectangle.
pub fn board(c: &mut Canvas<'_>, f: &BoardFrame<'_>) -> Vec<(Rect, usize)> {
    let b = f.doc.board();
    let mut hits = Vec::new();
    if b.pieces.is_empty() {
        c.centered(
            i32::from(c.area.x + c.area.width / 2),
            i32::from(c.area.y + c.area.height / 2),
            "Empty board. Load DSL with `blockies new`, or press ? for keys.",
            Style::default().fg(rgb(MUTED)),
        );
        return hits;
    }
    // Fit every placed piece (and token arcs) into the area.
    let (mut x0, mut y0, mut x1, mut y1) = (f32::MAX, f32::MAX, f32::MIN, f32::MIN);
    for p in b.pieces.iter().filter(|p| p.kind.is_placed()) {
        let ((x, y), (w, h)) = (centre(p), world_size(p));
        x0 = x0.min(x - w / 2.0);
        x1 = x1.max(x + w / 2.0);
        y0 = y0.min(y - h / 2.0 - if p.kind == Kind::Box { 60.0 } else { 0.0 });
        y1 = y1.max(y + h / 2.0);
    }
    let (pad_x, pad_y) = (30.0, 30.0);
    let (cols, rows) = (
        f32::from(c.area.width.max(10)),
        f32::from(c.area.height.max(5)),
    );
    let k = ((x1 - x0 + 2.0 * pad_x) / cols)
        .max((y1 - y0 + 2.0 * pad_y) / (2.0 * rows))
        .max(4.0);
    let xf = Xf {
        k,
        ox: (x0 + x1) / 2.0,
        oy: (y0 + y1) / 2.0,
        ax: f32::from(c.area.x) + cols / 2.0,
        ay: f32::from(c.area.y) + rows / 2.0,
    };
    let rect_of = |p: &Piece| -> (i32, i32, i32, i32) {
        let ((x, y), (w, h)) = (centre(p), world_size(p));
        let (cx, cy) = xf.cell(x, y);
        let min_w = match p.kind {
            Kind::Box => p.label.chars().count() as i32 + 4,
            Kind::Card => 16,
            _ => 4,
        };
        let cw = ((w / k).round() as i32).max(min_w);
        let ch =
            ((h / (2.0 * k)).round() as i32).max(if matches!(p.kind, Kind::Box | Kind::Card) {
                4
            } else {
                1
            });
        (cx - cw / 2, cy - ch / 2, cw, ch)
    };

    for (i, p) in b
        .pieces
        .iter()
        .enumerate()
        .filter(|(_, p)| p.kind == Kind::Arrow)
    {
        let (Some(a), Some(z)) = (
            p.from.as_deref().and_then(|id| b.piece(id)),
            p.to.as_deref().and_then(|id| b.piece(id)),
        ) else {
            continue;
        };
        arrow(
            c,
            p,
            rect_of(a),
            rect_of(z),
            f.t[i],
            f.phase[i],
            f.selected == Some(i),
        );
    }
    for (i, p) in b.pieces.iter().enumerate() {
        let r = rect_of(p);
        let sel = f.selected == Some(i);
        match p.kind {
            Kind::Box => draw_box(c, p, r, f.t[i], sel),
            Kind::Switch => draw_switch(c, p, r, f.t[i], sel),
            Kind::Slider => draw_slider(c, p, r, f.t[i], sel),
            Kind::Bar => draw_bar(c, p, r, f.t[i], sel),
            Kind::Card => draw_card(c, p, r, f.t[i], sel),
            Kind::Note => draw_note(c, p, r),
            Kind::Arrow | Kind::Token => continue,
        }
        hits.push((to_rect(r), i));
    }
    for (i, p) in b
        .pieces
        .iter()
        .enumerate()
        .filter(|(_, p)| p.kind == Kind::Token)
    {
        let (x, y) = token_pos(f.doc, p, f.t[i]);
        let (cx, cy) = xf.cell(x, y);
        let label: String = p.label.chars().take(18).collect();
        let text = format!(" ● {label} ");
        let w = text.chars().count() as i32;
        let style = Style::default()
            .fg(Color::Black)
            .bg(rgb(if f.selected == Some(i) {
                (250, 250, 250)
            } else {
                (60, 196, 200)
            }));
        c.text(
            cx - w / 2,
            cy - 1,
            &text,
            style.add_modifier(Modifier::BOLD),
        );
        hits.push((to_rect((cx - w / 2, cy - 1, w, 1)), i));
    }
    hits
}

fn to_rect((x, y, w, h): (i32, i32, i32, i32)) -> Rect {
    let c = |v: i32| v.clamp(0, i32::from(u16::MAX)) as u16;
    Rect::new(c(x), c(y), c(w), c(h))
}

fn sel_style(rgb_: Rgb, sel: bool) -> Style {
    let s = Style::default().fg(rgb(rgb_));
    if sel {
        s.add_modifier(Modifier::BOLD | Modifier::REVERSED)
    } else {
        s
    }
}

fn dots(c: &mut Canvas<'_>, cx: i32, y: i32, n: usize, t: f32, col: Rgb) {
    if n < 2 {
        return;
    }
    let start = cx - (2 * n as i32 - 1) / 2;
    for k in 0..n {
        let near = (1.0 - (t - k as f32).abs()).clamp(0.0, 1.0);
        let glyph = if near > 0.5 { '●' } else { '·' };
        c.ch(
            start + 2 * k as i32,
            y,
            glyph,
            Style::default().fg(rgb(mix(DIM, col, near))),
        );
    }
}

fn draw_box(c: &mut Canvas<'_>, p: &Piece, (x, y, w, h): (i32, i32, i32, i32), t: f32, sel: bool) {
    let col = state_rgb(p, t);
    c.clear(x, y, w, h);
    c.frame(
        x,
        y,
        w,
        h,
        Style::default().fg(rgb(col)).add_modifier(if sel {
            Modifier::BOLD
        } else {
            Modifier::empty()
        }),
        sel,
    );
    let cx = x + w / 2;
    c.centered(
        cx,
        y + 1,
        &p.label,
        Style::default().add_modifier(Modifier::BOLD),
    );
    let state = &p.states[(t.round().max(0.0) as usize).min(p.last_state())];
    c.centered(cx, y + 2, state, Style::default().fg(rgb(col)));
    if h >= 5 {
        dots(c, cx, y + h - 2, p.states.len(), t, col);
    }
}

fn draw_switch(
    c: &mut Canvas<'_>,
    p: &Piece,
    (x, y, w, _): (i32, i32, i32, i32),
    t: f32,
    sel: bool,
) {
    let n = p.last_state().max(1) as f32;
    let col = state_rgb(p, t);
    let cx = x + w / 2;
    c.centered(cx, y - 1, &p.label, Style::default().fg(rgb(MUTED)));
    let track = 6;
    let knob = ((t / n) * (track - 1) as f32).round() as i32;
    let left = cx - track / 2 - 1;
    c.ch(left, y, '(', sel_style(col, sel));
    for i in 0..track {
        let g = if i == knob { '●' } else { '─' };
        c.ch(
            left + 1 + i,
            y,
            g,
            Style::default().fg(rgb(if i == knob { col } else { DIM })),
        );
    }
    c.ch(left + track + 1, y, ')', sel_style(col, sel));
    let state = &p.states[(t.round().max(0.0) as usize).min(p.last_state())];
    c.centered(cx, y + 1, state, Style::default().fg(rgb(col)));
}

fn draw_slider(
    c: &mut Canvas<'_>,
    p: &Piece,
    (x, y, w, _): (i32, i32, i32, i32),
    t: f32,
    sel: bool,
) {
    let n = p.last_state().max(1) as f32;
    let col = state_rgb(p, t);
    let cx = x + w / 2;
    let track = w.clamp(12, 40);
    let left = cx - track / 2;
    c.centered(cx, y - 1, &p.label, Style::default().fg(rgb(MUTED)));
    let knob = ((t / n) * (track - 1) as f32).round() as i32;
    for i in 0..track {
        let (g, cc) = if i == knob {
            ('●', col)
        } else if i < knob {
            ('━', col)
        } else {
            ('─', DIM)
        };
        c.ch(
            left + i,
            y,
            g,
            if i == knob {
                sel_style(cc, sel)
            } else {
                Style::default().fg(rgb(cc))
            },
        );
    }
    let state = &p.states[(t.round().max(0.0) as usize).min(p.last_state())];
    c.centered(left + knob, y + 1, state, Style::default().fg(rgb(col)));
}

fn draw_bar(c: &mut Canvas<'_>, p: &Piece, (x, y, w, h): (i32, i32, i32, i32), t: f32, sel: bool) {
    let n = p.last_state();
    let t = t.clamp(0.0, n as f32);
    let i = (t.floor() as usize).min(n.saturating_sub(1));
    let f = t - i as f32;
    let v = |k: usize| p.values[k.min(n)].clamp(0, 100) as f32;
    let value = v(i) + (v(i + 1) - v(i)) * if n == 0 { 0.0 } else { f };
    let col = if value < 50.0 {
        (84, 200, 120)
    } else if value < 85.0 {
        (232, 170, 60)
    } else {
        (232, 84, 84)
    };
    let cx = x + w / 2;
    let height = h.clamp(3, 8);
    let top = y + (h - height) / 2;
    const EIGHTHS: [char; 9] = [' ', '▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'];
    let filled = value / 100.0 * height as f32;
    for r in 0..height {
        let from_bottom = (height - 1 - r) as f32;
        let level = ((filled - from_bottom).clamp(0.0, 1.0) * 8.0).round() as usize;
        for dx in -1..=1 {
            let g = if level == 0 { '░' } else { EIGHTHS[level] };
            c.ch(
                cx + dx,
                top + r,
                g,
                Style::default().fg(rgb(if level == 0 { DIM } else { col })),
            );
        }
    }
    c.centered(cx, top - 1, &format!("{value:.0}%"), sel_style(col, sel));
    c.centered(cx, top + height, &p.label, Style::default().fg(rgb(MUTED)));
}

fn draw_card(c: &mut Canvas<'_>, p: &Piece, (x, y, w, h): (i32, i32, i32, i32), t: f32, sel: bool) {
    // Flip: the card narrows to nothing at t = 0.5 and opens on the other side.
    let open = (1.0 - 2.0 * t.clamp(0.0, 1.0)).abs();
    let ww = ((w as f32 * open).round() as i32).max(2);
    let x = x + (w - ww) / 2;
    let back = t >= 0.5;
    let col = if back {
        (172, 124, 255)
    } else {
        (110, 150, 255)
    };
    c.clear(x, y, ww, h);
    c.frame(
        x,
        y,
        ww,
        h,
        Style::default().fg(rgb(col)).add_modifier(if sel {
            Modifier::BOLD
        } else {
            Modifier::empty()
        }),
        back,
    );
    if ww < 8 {
        return;
    }
    let text = if back {
        p.back.as_deref().unwrap_or("")
    } else {
        p.label.as_str()
    };
    for (r, line) in wrap(text, (ww - 4) as usize)
        .iter()
        .take((h - 2).max(0) as usize)
        .enumerate()
    {
        c.centered(
            x + ww / 2,
            y + 1 + r as i32,
            line,
            Style::default().add_modifier(if back {
                Modifier::ITALIC
            } else {
                Modifier::BOLD
            }),
        );
    }
}

fn draw_note(c: &mut Canvas<'_>, p: &Piece, (x, y, w, _): (i32, i32, i32, i32)) {
    for (r, line) in wrap(&p.label, (w - 2).max(12) as usize).iter().enumerate() {
        c.ch(x, y + r as i32, '▌', Style::default().fg(rgb(NOTE)));
        c.text(x + 2, y + r as i32, line, Style::default().fg(rgb(NOTE)));
    }
}

fn wrap(s: &str, max: usize) -> Vec<String> {
    let mut out = Vec::new();
    for para in s.split('\n') {
        let mut cur = String::new();
        for word in para.split(' ') {
            if !cur.is_empty() && cur.chars().count() + 1 + word.chars().count() > max {
                out.push(std::mem::take(&mut cur));
            }
            if !cur.is_empty() {
                cur.push(' ');
            }
            cur.push_str(word);
        }
        out.push(cur);
    }
    out
}

/// Point where the segment from the centre of `r` towards (tx, ty) leaves the rectangle.
fn edge(r: (i32, i32, i32, i32), tx: f32, ty: f32) -> (f32, f32) {
    let (cx, cy) = (r.0 as f32 + r.2 as f32 / 2.0, r.1 as f32 + r.3 as f32 / 2.0);
    let (dx, dy) = (tx - cx, ty - cy);
    if dx == 0.0 && dy == 0.0 {
        return (cx, cy);
    }
    let s = ((r.2 as f32 / 2.0 + 1.0) / dx.abs().max(1e-6))
        .min((r.3 as f32 / 2.0 + 0.5) / dy.abs().max(1e-6));
    (cx + dx * s, cy + dy * s)
}

fn arrow(
    c: &mut Canvas<'_>,
    p: &Piece,
    a: (i32, i32, i32, i32),
    z: (i32, i32, i32, i32),
    t: f32,
    phase: f32,
    sel: bool,
) {
    let (acx, acy) = (a.0 as f32 + a.2 as f32 / 2.0, a.1 as f32 + a.3 as f32 / 2.0);
    let (zcx, zcy) = (z.0 as f32 + z.2 as f32 / 2.0, z.1 as f32 + z.3 as f32 / 2.0);
    let (sx, sy) = edge(a, zcx, zcy);
    let (ex, ey) = edge(z, acx, acy);
    let (dx, dy) = (ex - sx, ey - sy);
    // Cells are ~2x taller than wide; compare in square units to pick the glyph.
    let (ux, uy) = (dx, dy * 2.0);
    let glyph = if ux.abs() > 2.0 * uy.abs() {
        '─'
    } else if uy.abs() > 2.0 * ux.abs() {
        '│'
    } else if (ux > 0.0) == (uy > 0.0) {
        '╲'
    } else {
        '╱'
    };
    let head = if ux.abs() >= uy.abs() {
        if dx > 0.0 { '▶' } else { '◀' }
    } else if dy > 0.0 {
        '▼'
    } else {
        '▲'
    };
    let on = t > 0.5;
    let col = if on { state_rgb(p, t) } else { DIM };
    let steps = dx.abs().max(dy.abs()).ceil().max(1.0) as i32;
    for s in 0..=steps {
        let f = s as f32 / steps as f32;
        let (x, y) = ((sx + dx * f).round() as i32, (sy + dy * f).round() as i32);
        let flow = on && ((f * steps as f32 / 4.0 - phase * 1.0).rem_euclid(1.0) < 0.25);
        let g = if s == steps {
            head
        } else if flow {
            '•'
        } else {
            glyph
        };
        let style = Style::default().fg(rgb(if flow {
            mix(col, (255, 255, 255), 0.35)
        } else {
            col
        }));
        c.ch(
            x,
            y,
            g,
            if sel {
                style.add_modifier(Modifier::BOLD | Modifier::REVERSED)
            } else {
                style
            },
        );
    }
    if !p.label.is_empty() {
        let (mx, my) = (
            (sx + dx / 2.0).round() as i32,
            (sy + dy / 2.0).round() as i32 - 1,
        );
        c.centered(
            mx,
            my,
            &p.label,
            Style::default()
                .fg(rgb(MUTED))
                .add_modifier(Modifier::ITALIC),
        );
    }
}

/// Viewport over the block field.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct FieldView {
    /// Field coordinates of the top-left pixel.
    pub ox: i32,
    pub oy: i32,
    /// Field cells per pixel (powers of two).
    pub zoom: u32,
}

impl FieldView {
    /// Smallest zoom that shows the whole field, centred.
    pub fn fit(world: &World, area: Rect) -> Self {
        let (w, h) = world.size();
        let (pw, ph) = (
            usize::from(area.width.max(1)),
            usize::from(area.height.max(1)) * 2,
        );
        let mut zoom = 1u32;
        while (w.div_ceil(zoom as usize) > pw || h.div_ceil(zoom as usize) > ph) && zoom < 64 {
            zoom *= 2;
        }
        let z = zoom as i32;
        Self {
            ox: (w as i32 - pw as i32 * z) / 2,
            oy: (h as i32 - ph as i32 * z) / 2,
            zoom,
        }
    }

    /// Field cell under a terminal cell (top pixel).
    pub fn at(&self, area: Rect, col: u16, row: u16) -> (i32, i32) {
        let z = self.zoom as i32;
        (
            self.ox + i32::from(col.saturating_sub(area.x)) * z,
            self.oy + i32::from(row.saturating_sub(area.y)) * 2 * z,
        )
    }
}

pub fn field(c: &mut Canvas<'_>, world: &World, v: FieldView) {
    let (w, h) = world.size();
    let z = v.zoom as i32;
    let colour = |px: i32, py: i32| -> Rgb {
        let (fx, fy) = (v.ox + px * z, v.oy + py * z);
        if fx < 0 || fy < 0 || fx >= w as i32 || fy >= h as i32 {
            return (8, 8, 10);
        }
        let (fx, fy) = (fx as usize, fy as usize);
        let m = world.sample(fx, fy, fx + z as usize, fy + z as usize);
        if m == 0 {
            FIELD_BG
        } else {
            let p = PALETTE[usize::from(m)];
            (p[0], p[1], p[2])
        }
    };
    for row in 0..i32::from(c.area.height) {
        for col in 0..i32::from(c.area.width) {
            let (top, bot) = (colour(col, row * 2), colour(col, row * 2 + 1));
            let (x, y) = (i32::from(c.area.x) + col, i32::from(c.area.y) + row);
            c.ch(x, y, '▀', Style::default().fg(rgb(top)).bg(rgb(bot)));
        }
    }
}
