// Concept 8: Nest. A computer can contain a whole world, and that world can
// contain computers with worlds, as deep as needed. Every level stays small
// (a handful of blocks), so complexity comes from depth, not crowding. The
// outer view shows a live miniature of each inner world; click to go in.

export default {
  title: 'Nest',
  tagline: 'a computer can contain a world; complexity comes from depth, each level stays small',
  rate: 4,
  idea: `
<h3>The idea</h3>
<p>Edward wants any complexity, and every other experiment got crowded fast. Here a computer may <b>contain a world</b>. That world has its own lattice (any rank) and its own computers, which may contain worlds again.</p>
<ul>
<li>From outside, a computer with a world shows a <b>live miniature</b> of what is inside, so you see that something is busy without going in.</li>
<li>Click it to <b>go in</b>. Press Esc, or click empty space, to come back out.</li>
<li>Each level stays at about 3 to 7 blocks: big systems become deep, not dense. An agent can address anything by path: <code>company/eng/api/login</code>.</li>
</ul>
<h3>Try</h3>
<p>Go into <b>eng</b>, then <b>api</b>, then <b>login</b>. The blinking colours are each computer's state; the miniatures blink in sync with the inside.</p>`,
  notes: `
<h3>What worked</h3>
<ul>
<li><b>Depth keeps every view readable.</b> Four levels and 25 computers, yet no screen has more than six blocks.</li>
<li><b>Miniatures are the best part.</b> From the top you can see that something inside "eng" is red without going in. It is a summary that the renderer gives for free, because the inner world is just another lattice.</li>
<li><b>Paths are a natural address</b> for agents and for voice: "show me eng, api, login".</li>
</ul>
<h3>Open questions</h3>
<ul>
<li>How do messages cross levels? Probably only through the containing computer (it is the inner world's parent), which keeps the rule "share only through messages".</li>
<li>Should an inner world's rank be free? Here a 2D company can contain a 1D pipeline; that already works in the lattice.</li>
<li>Going in is a hard cut. A real zoom animation (the miniature growing into the full world) would make the hierarchy feel physical.</li>
</ul>`,
  spec: `
{
  label: 'company', world: [5, 3],
  computers: [
    { label: 'eng', at: [1, 1], color: '#3b6ff5', world: [5, 4], computers: [
      { label: 'web', at: [0, 1], color: '#3b6ff5', states: ['#3b6ff5', '#7a9cff'], period: 3 },
      { label: 'api', at: [2, 1], color: '#e0524f', world: [4, 4], computers: [
        { label: 'login', at: [0, 0], color: '#2e9e6e', world: [5, 2], computers: [
          { label: 'validate', at: [0, 0], states: ['#2e9e6e', '#f2a33a'], period: 2 },
          { label: 'hash', at: [2, 0], states: ['#2e9e6e', '#2e9e6e', '#e0524f'], period: 2 },
          { label: 'session', at: [4, 0], states: ['#2e9e6e', '#7a5af5'], period: 3 },
          { label: 'audit', at: [2, 1], states: ['#8a90a0'], period: 1 },
        ] },
        { label: 'search', at: [2, 0], states: ['#2e9e6e', '#f2a33a', '#e0524f'], period: 3 },
        { label: 'pay', at: [0, 2], states: ['#2e9e6e'], period: 1 },
        { label: 'cache', at: [2, 2], states: ['#7a5af5', '#a58cff'], period: 2 },
      ] },
      { label: 'db', at: [4, 1], color: '#2e9e6e', states: ['#2e9e6e', '#2e9e6e', '#f2a33a'], period: 4 },
      { label: 'queue', at: [2, 3], color: '#f2a33a', world: [8, 1], computers: [
        { label: 'j1', at: [0, 0], states: ['#f2a33a', '#e0524f'], period: 2 },
        { label: 'j2', at: [2, 0], states: ['#f2a33a'], period: 1 },
        { label: 'j3', at: [4, 0], states: ['#f2a33a', '#8a90a0'], period: 3 },
        { label: 'j4', at: [6, 0], states: ['#8a90a0'], period: 1 },
      ] },
    ] },
    { label: 'sales', at: [3, 0], color: '#f2a33a', world: [3, 3], computers: [
      { label: 'leads', at: [0, 0], states: ['#f2a33a', '#ffcc33'], period: 3 },
      { label: 'deals', at: [2, 2], states: ['#2e9e6e', '#f2a33a'], period: 5 },
    ] },
    { label: 'ops', at: [3, 2], color: '#2e9e6e', world: [3, 3], computers: [
      { label: 'deploy', at: [0, 1], states: ['#2e9e6e', '#3b6ff5'], period: 4 },
      { label: 'alerts', at: [2, 1], states: ['#2e9e6e', '#2e9e6e', '#2e9e6e', '#e0524f'], period: 2 },
    ] },
  ],
}`,

  create(spec) {
    let time = 0;
    const path = [];
    const node = () => path.reduce((n, i) => n.computers[i], spec);
    const colorOf = (c, idx) => {
      if (!c.states) return c.color ?? '#8a90a0';
      const k = Math.floor((time + idx * 3) / (c.period ?? 2)) % c.states.length;
      return c.states[k];
    };
    // A world "looks" red if anything inside it is red (a summary for the miniature).
    const items = () =>
      node().computers.map((c, i) => ({
        id: path.join('/') + '/' + i,
        owner: String(i),
        at: c.at,
        kind: 'c',
        color: c.world ? c.color ?? '#8a90a0' : colorOf(c, i),
        label: c.label,
        inner: c.world ? c.computers.map((k, j) => ({ at: k.at, color: k.world ? k.color ?? '#8a90a0' : colorOf(k, j) })) : undefined,
        innerShape: c.world,
      }));
    return {
      get shape() {
        return node().world;
      },
      get time() {
        return time;
      },
      tick() {
        time++;
      },
      items,
      processes() {
        const crumbs = ['company', ...path.map((_, k) => path.slice(0, k + 1).reduce((n, i) => n.computers[i], spec).label)];
        return [
          { id: 'path', label: crumbs.join(' / '), status: 'wait', note: path.length ? 'Esc to go up' : '' },
          ...node().computers.map((c, i) => ({ id: String(i), label: c.label, status: 'run', note: c.world ? `${c.computers.length} inside` : '' })),
        ];
      },
      click(at) {
        const n = node();
        const i = n.computers.findIndex((c) => c.at.every((v, k) => v === at[k]));
        if (i >= 0 && n.computers[i].world) path.push(i);
        else if (i < 0 && path.length) path.pop();
      },
      up() {
        path.pop();
      },
      status() {
        return 'depth ' + path.length + ' · click a computer with a world to go in, Esc or empty space to come out';
      },
    };
  },
};
