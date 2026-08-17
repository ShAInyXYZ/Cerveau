// One tooltip for the whole app, as a Svelte action — attach to ANY element with
// `use:tooltip={'label'}` (or `use:tooltip={{ text, placement, delay }}`). No
// wrapping component needed. A single shared DOM node is reused for every tooltip,
// so there's exactly one themed tooltip element on the page at a time.
//
// Replaces the browser's default title= (slow, unstyled, inconsistent).

let el = null;          // the shared floating tooltip node
let arrow = null;
let hideTimer = null;

function ensureNode() {
  if (el) return;
  el = document.createElement('div');
  el.className = 'app-tooltip';
  el.setAttribute('role', 'tooltip');
  arrow = document.createElement('span');
  arrow.className = 'app-tooltip-arrow';
  el.appendChild(arrow);
  document.body.appendChild(el);
  injectStyles();
}

function injectStyles() {
  if (document.getElementById('app-tooltip-style')) return;
  const s = document.createElement('style');
  s.id = 'app-tooltip-style';
  s.textContent = `
    .app-tooltip {
      position: fixed; z-index: 9999; pointer-events: none;
      padding: 5px 9px; border-radius: 7px;
      background: var(--s3, #262320); color: var(--text, #ece8e0);
      font-family: var(--font-sans, system-ui, sans-serif);
      font-size: 11.5px; line-height: 1.2; white-space: nowrap;
      box-shadow: 0 0 0 1px var(--line2, #34302b), 0 8px 18px -8px rgba(0,0,0,.7);
      opacity: 0; transform: translateY(2px);
      transition: opacity .12s ease, transform .12s ease;
    }
    .app-tooltip.show { opacity: 1; transform: none; }
    .app-tooltip-arrow {
      position: absolute; width: 7px; height: 7px;
      background: var(--s3, #262320); transform: rotate(45deg);
      box-shadow: 0 0 0 1px var(--line2, #34302b);
    }
    @media (prefers-reduced-motion: reduce) {
      .app-tooltip { transition: none; }
    }
  `;
  document.head.appendChild(s);
}

function place(target, placement) {
  const r = target.getBoundingClientRect();
  const tw = el.offsetWidth, th = el.offsetHeight;
  const gap = 8;
  let x, y;
  // reset arrow
  arrow.style.cssText = 'position:absolute;width:7px;height:7px;background:var(--s3,#262320);transform:rotate(45deg);box-shadow:0 0 0 1px var(--line2,#34302b);';

  switch (placement) {
    case 'bottom':
      x = r.left + r.width / 2 - tw / 2; y = r.bottom + gap;
      arrow.style.top = '-3.5px'; arrow.style.left = (tw / 2 - 3.5) + 'px';
      arrow.style.clipPath = 'polygon(0 0,100% 0,0 100%)';
      break;
    case 'left':
      x = r.left - tw - gap; y = r.top + r.height / 2 - th / 2;
      arrow.style.right = '-3.5px'; arrow.style.top = (th / 2 - 3.5) + 'px';
      arrow.style.clipPath = 'polygon(0 0,100% 100%,0 100%)';
      break;
    case 'right':
      x = r.right + gap; y = r.top + r.height / 2 - th / 2;
      arrow.style.left = '-3.5px'; arrow.style.top = (th / 2 - 3.5) + 'px';
      arrow.style.clipPath = 'polygon(100% 0,100% 100%,0 0)';
      break;
    default: // top
      x = r.left + r.width / 2 - tw / 2; y = r.top - th - gap;
      arrow.style.bottom = '-3.5px'; arrow.style.left = (tw / 2 - 3.5) + 'px';
      arrow.style.clipPath = 'polygon(100% 0,100% 100%,0 100%)';
  }
  // keep on-screen horizontally
  x = Math.max(6, Math.min(x, window.innerWidth - tw - 6));
  el.style.left = Math.round(x) + 'px';
  el.style.top = Math.round(y) + 'px';
}

export function tooltip(node, opts) {
  let cfg = normalize(opts);

  function normalize(o) {
    if (!o) return { text: '', placement: 'top', delay: 400 };
    if (typeof o === 'string') return { text: o, placement: 'top', delay: 400 };
    return { text: o.text ?? '', placement: o.placement ?? 'top', delay: o.delay ?? 400 };
  }

  let showTimer = null;

  function show() {
    if (!cfg.text) return;
    showTimer = setTimeout(() => {
      ensureNode();
      if (hideTimer) { clearTimeout(hideTimer); hideTimer = null; }
      el.textContent = cfg.text;   // set label
      el.appendChild(arrow);       // re-attach the arrow after clearing
      place(node, cfg.placement);
      // re-place after layout (width known) then reveal
      requestAnimationFrame(() => { place(node, cfg.placement); el.classList.add('show'); });
    }, cfg.delay);
  }
  function hide() {
    if (showTimer) { clearTimeout(showTimer); showTimer = null; }
    if (el) {
      el.classList.remove('show');
      hideTimer = setTimeout(() => { if (el) el.style.top = '-9999px'; }, 140);
    }
  }

  // Touch devices synthesise mouseenter AFTER a tap and never send
  // mouseleave, so a tooltip shown that way stays painted on screen —
  // it sat on top of the workspace picker until the view changed.
  // A pointer event tells us which kind of input this is.
  let coarse = false;
  function onPointerDown(e) { coarse = e.pointerType !== 'mouse'; }
  function showIfHover() { if (!coarse) show(); }

  node.addEventListener('pointerdown', onPointerDown);
  node.addEventListener('mouseenter', showIfHover);
  node.addEventListener('mouseleave', hide);
  node.addEventListener('focusin', show);
  node.addEventListener('focusout', hide);
  node.addEventListener('click', hide); // dismiss on activate

  return {
    update(o) { cfg = normalize(o); },
    destroy() {
      hide();
      node.removeEventListener('pointerdown', onPointerDown);
      node.removeEventListener('mouseenter', showIfHover);
      node.removeEventListener('mouseleave', hide);
      node.removeEventListener('focusin', show);
      node.removeEventListener('focusout', hide);
      node.removeEventListener('click', hide);
    }
  };
}
