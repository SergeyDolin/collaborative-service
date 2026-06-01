// CPS — Topographic background motif
// Replaces the old coordinate-grid backdrop with map-style isolines
// (contour lines extracted from a smooth scalar field via marching squares)
// plus a few schematic satellites broadcasting signal beams onto the terrain.
//
// Self-injecting + idempotent. Sits in a fixed, pointer-events:none layer
// behind all page content. Loaded globally from chrome.js.

(function () {
  'use strict';

  var VW = 1600, VH = 1000, STEP = 20;
  var COLS = Math.round(VW / STEP), ROWS = Math.round(VH / STEP);

  // ── Terrain field: a sum of gaussian "hills" and "basins" + gentle warp.
  //    Hand-tuned so contours read like a real topo map (ridges, a saddle,
  //    one closed depression) rather than concentric ellipses. ────────────
  var PEAKS = [
    { x: 360,  y: 300, amp:  1.00, s: 250 },
    { x: 1200, y: 230, amp:  0.82, s: 300 },
    { x: 770,  y: 660, amp:  1.18, s: 360 },
    { x: 1360, y: 800, amp: -0.62, s: 270 },  // basin
    { x: 150,  y: 800, amp:  0.58, s: 230 },
    { x: 980,  y: 980, amp: -0.40, s: 240 }   // basin
  ];

  function field(x, y) {
    var h = 0;
    for (var i = 0; i < PEAKS.length; i++) {
      var p = PEAKS[i];
      var dx = x - p.x, dy = y - p.y;
      h += p.amp * Math.exp(-(dx * dx + dy * dy) / (2 * p.s * p.s));
    }
    // low-frequency warp so isolines wiggle organically
    h += 0.10 * Math.sin(x / 155) * Math.cos(y / 185);
    h += 0.05 * Math.sin((x + y) / 240);
    return h;
  }

  // sample grid
  var G = [], mn = Infinity, mx = -Infinity;
  for (var r = 0; r <= ROWS; r++) {
    G[r] = [];
    for (var c = 0; c <= COLS; c++) {
      var v = field(c * STEP, r * STEP);
      G[r][c] = v;
      if (v < mn) mn = v;
      if (v > mx) mx = v;
    }
  }

  // ── Marching squares → one SVG path string per contour level ───────────
  function contourPath(level) {
    var d = '';
    for (var r = 0; r < ROWS; r++) {
      for (var c = 0; c < COLS; c++) {
        var x0 = c * STEP, y0 = r * STEP, x1 = x0 + STEP, y1 = y0 + STEP;
        var tl = G[r][c], tr = G[r][c + 1], br = G[r + 1][c + 1], bl = G[r + 1][c];
        var idx = 0;
        if (tl > level) idx |= 8;
        if (tr > level) idx |= 4;
        if (br > level) idx |= 2;
        if (bl > level) idx |= 1;
        if (idx === 0 || idx === 15) continue;

        function top()    { return [x0 + STEP * (level - tl) / (tr - tl), y0]; }
        function right()  { return [x1, y0 + STEP * (level - tr) / (br - tr)]; }
        function bottom() { return [x0 + STEP * (level - bl) / (br - bl), y1]; }
        function left()   { return [x0, y0 + STEP * (level - tl) / (bl - tl)]; }
        function seg(a, b) {
          d += 'M' + a[0].toFixed(1) + ' ' + a[1].toFixed(1) +
               'L' + b[0].toFixed(1) + ' ' + b[1].toFixed(1);
        }
        switch (idx) {
          case 1:  seg(left(), bottom()); break;
          case 2:  seg(bottom(), right()); break;
          case 3:  seg(left(), right()); break;
          case 4:  seg(top(), right()); break;
          case 5:  seg(top(), right()); seg(left(), bottom()); break;
          case 6:  seg(top(), bottom()); break;
          case 7:  seg(top(), left()); break;
          case 8:  seg(top(), left()); break;
          case 9:  seg(top(), bottom()); break;
          case 10: seg(top(), left()); seg(bottom(), right()); break;
          case 11: seg(top(), right()); break;
          case 12: seg(left(), right()); break;
          case 13: seg(bottom(), right()); break;
          case 14: seg(left(), bottom()); break;
        }
      }
    }
    return d;
  }

  // ── Build contour paths ────────────────────────────────────────────────
  var LEVELS = 15;
  var contours = '';
  var sigLevel = Math.round(LEVELS * 0.62); // one highlighted "index" ridge
  for (var i = 1; i < LEVELS; i++) {
    var lv = mn + (mx - mn) * i / LEVELS;
    var d = contourPath(lv);
    if (!d) continue;
    var cls = 'contour';
    if (i % 3 === 0) cls += ' index';
    if (i === sigLevel) cls += ' sig';
    contours += '<path class="' + cls + '" d="' + d + '"/>';
  }

  // ── Schematic satellite (line-art, matches CPS device glyph language) ──
  //    Returns a positioned + float-animated group. `accent` swaps the body
  //    indicator + beam to the lime signal colour. ─────────────────────────
  function satellite(opt) {
    var x = opt.x, y = opt.y, s = opt.s || 1, rot = opt.rot || 0;
    var delay = opt.delay || 0, dur = opt.dur || 9;
    var accent = opt.accent ? ' accent' : '';
    var bodyDot = opt.accent
      ? '<circle cx="0" cy="0" r="2.6" class="sig-fill"/>'
      : '<circle cx="0" cy="0" r="2.6" fill="currentColor"/>';

    // solar-panel internal grid
    function panel(px) {
      var g = '<rect x="' + px + '" y="-11" width="30" height="22" rx="1" fill="var(--card,#fbf9f3)"/>';
      for (var k = 1; k < 4; k++) {
        var lx = (px + k * 30 / 4).toFixed(1);
        g += '<line x1="' + lx + '" y1="-11" x2="' + lx + '" y2="11" class="hair"/>';
      }
      g += '<line x1="' + px + '" y1="0" x2="' + (px + 30) + '" y2="0" class="hair"/>';
      return g;
    }

    return (
      '<g class="sat-pos" transform="translate(' + x + ' ' + y + ') scale(' + s + ') rotate(' + rot + ')">' +
        '<g class="sat-float' + accent + '" style="animation-delay:' + delay + 's;animation-duration:' + dur + 's">' +
          // booms
          '<line x1="-18" y1="0" x2="-11" y2="0" class="strut"/>' +
          '<line x1="18" y1="0" x2="11" y2="0" class="strut"/>' +
          // solar wings
          '<g class="strut">' + panel(-48) + panel(18) + '</g>' +
          // body
          '<rect x="-11" y="-9" width="22" height="18" rx="2" fill="var(--card,#fbf9f3)" class="strut"/>' +
          bodyDot +
          // dish on a short mast
          '<line x1="0" y1="-9" x2="0" y2="-15" class="strut"/>' +
          '<ellipse cx="0" cy="-17" rx="7" ry="3" fill="var(--card,#fbf9f3)" class="strut"/>' +
          '<line x1="0" y1="-17" x2="0" y2="-20" class="hair"/>' +
        '</g>' +
      '</g>'
    );
  }

  // ── Ground node (a positioning station the satellite locks onto) ───────
  function node(x, y, accent) {
    var ring = accent ? ' sig-ring' : '';
    var dot = accent ? 'sig-fill' : 'node-dot';
    return (
      '<g class="gnode" transform="translate(' + x + ' ' + y + ')">' +
        '<circle r="9" class="pulse' + ring + '"/>' +
        '<circle r="4.5" fill="var(--card,#fbf9f3)" class="strut"/>' +
        '<circle r="1.6" class="' + dot + '"/>' +
      '</g>'
    );
  }

  // ── Signal beam: a faint cone of dashed rays from a satellite to a node ─
  function beam(sx, sy, nx, ny, accent) {
    var ax = nx - 16, ay = ny;
    var bx = nx + 16, by = ny;
    var cls = accent ? 'beam accent' : 'beam';
    return (
      '<g class="' + cls + '">' +
        '<path d="M' + sx + ' ' + sy + 'L' + ax + ' ' + ay + 'L' + bx + ' ' + by + 'Z" class="beam-cone"/>' +
        '<line x1="' + sx + '" y1="' + sy + '" x2="' + nx + '" y2="' + ny + '" class="beam-ray"/>' +
      '</g>'
    );
  }

  // Composition: satellites up high, nodes on the terrain, two locked beams.
  var sats =
    satellite({ x: 300,  y: 170, s: 1.05, rot: -14, accent: true,  delay: 0,   dur: 10 }) +
    satellite({ x: 1180, y: 130, s: 0.92, rot: 12,  accent: false, delay: -3,  dur: 11 }) +
    satellite({ x: 860,  y: 250, s: 0.8,  rot: -6,  accent: false, delay: -6,  dur: 9  });

  var nodes  = node(380, 540, true) + node(1150, 470, false) + node(700, 720, false);
  var beams  = beam(300, 170, 380, 540, true) + beam(1180, 130, 1150, 470, false);

  var svg =
    '<svg viewBox="0 0 ' + VW + ' ' + VH + '" preserveAspectRatio="xMidYMid slice" aria-hidden="true">' +
      '<g class="topo-lines">' + contours + '</g>' +
      '<g class="topo-beams">' + beams + '</g>' +
      '<g class="topo-nodes">' + nodes + '</g>' +
      '<g class="topo-sats">' + sats + '</g>' +
    '</svg>';

  function mount() {
    if (document.querySelector('.cps-topo')) return;
    if (!document.body) return;
    var layer = document.createElement('div');
    layer.className = 'cps-topo';
    layer.setAttribute('aria-hidden', 'true');
    layer.innerHTML = svg;
    document.body.insertBefore(layer, document.body.firstChild);
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', mount);
  } else {
    mount();
  }
})();
