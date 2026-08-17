<script>
  // A dot-matrix / LED-panel readout (stadium-scoreboard style). Renders a short
  // string as a grid of dots: lit dots in the accent, unlit dots faint — like a
  // real LED display. SVG so it stays crisp at any size and recolors via tokens.
  let {
    text = 'V0.4',
    dot = 2.4,          // dot radius
    gap = 2.2,          // gap between dot centers beyond the diameter
    lit = 'var(--accent)',
    off = 'color-mix(in srgb, var(--accent) 14%, transparent)'
  } = $props();

  // 5x7 glyphs, rows top→bottom, each row a 5-char bitmask string.
  const G = {
    'V': ['10001','10001','10001','10001','01010','01010','00100'],
    '0': ['01110','10001','10011','10101','11001','10001','01110'],
    '1': ['00100','01100','00100','00100','00100','00100','01110'],
    '2': ['01110','10001','00001','00010','00100','01000','11111'],
    '3': ['01110','10001','00001','00110','00001','10001','01110'],
    '4': ['00010','00110','01010','10010','11111','00010','00010'],
    '5': ['11111','10000','11110','00001','00001','10001','01110'],
    '6': ['00110','01000','10000','11110','10001','10001','01110'],
    '7': ['11111','00001','00010','00100','01000','01000','01000'],
    '8': ['01110','10001','10001','01110','10001','10001','01110'],
    '9': ['01110','10001','10001','01111','00001','00010','01100'],
    '.': ['00000','00000','00000','00000','00000','00110','00110'],
    ' ': ['00000','00000','00000','00000','00000','00000','00000']
  };

  const COLS = 5, ROWS = 7;
  const step = $derived(dot * 2 + gap);

  // build a flat list of dots across all chars
  const dots = $derived.by(() => {
    const out = [];
    let ox = 0;
    for (const ch of text) {
      // '.' is a narrow glyph (2 cols) so it kerns tight, like a real panel
      const glyph = G[ch] ?? G[' '];
      const isDot = ch === '.';
      const cols = isDot ? 2 : COLS;
      for (let r = 0; r < ROWS; r++) {
        for (let c = 0; c < cols; c++) {
          const on = glyph[r][isDot ? c + 3 : c] === '1';
          out.push({
            x: ox + c * step + dot,
            y: r * step + dot,
            on
          });
        }
      }
      ox += cols * step + (isDot ? gap : step * 0.6); // char spacing
    }
    return out;
  });

  const width = $derived.by(() => (dots.length ? Math.max(...dots.map((d) => d.x)) + dot : 0));
  const height = ROWS * step - gap;
</script>

<svg class="matrix" width={width} height={height} viewBox="0 0 {width} {height}" role="img" aria-label={text}>
  {#each dots as d}
    <circle cx={d.x} cy={d.y} r={dot} fill={d.on ? lit : off} />
  {/each}
</svg>

<style>
  .matrix { display: block; }
</style>
