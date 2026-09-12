/* In-app visual picker. Runs in our first-party page (blocker-safe) and drives
 * the sandboxed /pick mirror through the iframe's same-origin DOM.
 * Exposes openPicker(url) / closePicker(). */
(function () {
  'use strict';

  var frame, crumbBar, panel;
  var pageUrl = '';
  var state = { armed: 'item', smart: true, item: '', title: 'a', link: 'a' };

  // ---------- selector generation (same heuristics as auto-detect) ----------
  function stableToken(el) {
    var cls = (el.className && el.className.baseVal !== undefined) ? el.className.baseVal : (el.className || '');
    var parts = String(cls).split(/\s+/);
    for (var i = 0; i < parts.length; i++) {
      var t = parts[i];
      if (!t || t.length < 2) continue;
      var l = t.toLowerCase();
      if (/^(active|current|open|selected|hover|focus)$/.test(l)) continue;
      if (/^css-[0-9a-z]+$/i.test(t)) continue;
      if (/^[0-9a-f]{6,}$/i.test(t)) continue;
      if (/^(ember|v-|ng-|sc-)/.test(t)) continue;
      return t;
    }
    return '';
  }
  function tag(el) { return el.tagName ? el.tagName.toLowerCase() : ''; }
  function selfSel(el) {
    var t = tag(el);
    if (!t || t === 'html' || t === 'body') return t;
    var tok = stableToken(el);
    return tok ? t + '.' + tok : t;
  }
  function sig(el) { return selfSel(el); }
  function siblingsSame(el) {
    var p = el.parentElement;
    if (!p) return 0;
    var s = sig(el), n = 0, kids = p.children;
    for (var i = 0; i < kids.length; i++) { if (sig(kids[i]) === s) n++; }
    return n;
  }
  function smartContainer(el) {
    var cur = el;
    while (cur && tag(cur) !== 'body' && tag(cur) !== 'html') {
      if (siblingsSame(cur) >= 3) return cur;
      cur = cur.parentElement;
    }
    return el;
  }
  function selectorFor(el, smart, d) {
    var target = smart ? smartContainer(el) : el;
    var t = tag(target), tok = stableToken(target);
    var sel;
    if (tok) sel = t + '.' + tok;
    else if (t === 'li' || t === 'div' || t === 'section' || t === 'article') {
      var p = target.parentElement;
      sel = (p && tag(p) !== 'body' ? selfSel(p) + ' > ' : '') + t;
    } else sel = t;
    // Disambiguate nesting containers (e.g. MUI grids): if matches nest
    // inside each other, qualify one level with the true parent so the
    // selector hits cards only, not outer layout grids.
    if (smart && d && isNested(d, sel)) {
      var par = target.parentElement;
      if (par && tag(par) !== 'body' && tag(par) !== 'html') sel = selfSel(par) + ' > ' + sel;
    }
    return sel;
  }
  // True when some matches contain further matches (ambiguous level).
  function isNested(d, sel) {
    try {
      var m = d.querySelectorAll(sel);
      if (m.length < 3 || m.length > 500) return false;
      var n = Math.min(m.length, 30);
      for (var i = 0; i < n; i++) {
        if (m[i].querySelector(sel)) return true;
      }
    } catch (e) {}
    return false;
  }
  function crumb(el) {
    var parts = [], cur = el, depth = 0;
    while (cur && tag(cur) !== 'body' && tag(cur) !== 'html' && depth < 6) {
      parts.unshift({ el: cur, label: selfSel(cur) });
      cur = cur.parentElement; depth++;
    }
    return parts;
  }
  function relativeSel(containerSel, el, doc) {
    try {
      var boxes = doc.querySelectorAll(containerSel);
      if (!boxes.length) return selfSel(el);
      var box = null, node = el;
      while (node && tag(node) !== 'body') {
        for (var i = 0; i < boxes.length; i++) { if (boxes[i] === node) { box = node; break; } }
        if (box) break;
        node = node.parentElement;
      }
      if (!box) return selfSel(el);
      var parts = [], cur = el;
      while (cur && cur !== box) { parts.unshift(selfSel(cur)); cur = cur.parentElement; }
      if (parts.length > 2) parts = parts.slice(parts.length - 2);
      return parts.join(' ') || 'a';
    } catch (e) { return 'a'; }
  }

  // Stretched-card helper: when item containers hold exactly one text-less
  // overlay anchor each (whole card is one link), return its selector so the
  // user doesn't have to hunt an invisible element. Majority vote across
  // containers; skips boxes containing nested item matches (outer grids).
  function stretchedLinkSel(d, itemSel) {
    try {
      var boxes = d.querySelectorAll(itemSel);
      if (!boxes.length) return '';
      var votes = {}, n = Math.min(boxes.length, 20);
      for (var b = 0; b < n; b++) {
        if (boxes[b].querySelector(itemSel)) continue;
        var as = boxes[b].querySelectorAll('a[href]');
        var single = null, empty = 0;
        for (var i = 0; i < as.length; i++) {
          var t = (as[i].textContent || '').trim();
          if (t === '' && !as[i].querySelector('img')) { empty++; single = as[i]; }
        }
        if (empty !== 1 || !single) continue;
        var s = selfSel(single);
        votes[s] = (votes[s] || 0) + 1;
      }
      var best = '', bestN = 0, total = 0;
      for (var k in votes) { total += votes[k]; if (votes[k] > bestN) { bestN = votes[k]; best = k; } }
      if (bestN >= 2 && bestN * 2 >= total) return best;
      return '';
    } catch (e) { return ''; }
  }

  // ---------- panel ----------
  function esc(s) {
    return String(s == null ? '' : s).replace(/[&<>"']/g, function (c) {
      return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c];
    });
  }
  function render(msg, samples) {
    var slots = ['item', 'title', 'link'].map(function (role) {
      var label = role === 'item' ? 'Item container' : role[0].toUpperCase() + role.slice(1);
      return '<div class="rspk-slot' + (state.armed === role ? ' armed' : '') + '" data-role="' + role + '">' +
        '<b>' + label + '</b><code>' + esc(state[role] || '— click page —') + '</code></div>';
    }).join('');
    var list = '';
    if (samples && samples.length) {
      list = '<ul>' + samples.map(function (x) {
        return '<li><a href="' + esc(x.URL) + '" target="_blank">' + esc(x.Title) + '</a></li>';
      }).join('') + '</ul>';
    }
    panel.innerHTML =
      '<h1>Pick selectors</h1>' +
      '<div class="rspk-hint">Hover to inspect precisely — ↑/↓, Parent/Child, or crumb to walk the tree. Click or Enter assigns.</div>' +
      slots +
      '<label class="rspk-row"><input type="checkbox" id="rspk-smart" ' + (state.smart ? 'checked' : '') + '> Smart container</label>' +
      '<div class="rspk-btns"><button id="rspk-auto">Auto-fill</button><button id="rspk-test">Test</button></div>' +
      '<div class="rspk-result" id="rspk-result">' + (msg || 'Pick an item container to start.') + list + '</div>' +
      '<div class="rspk-btns"><button id="rspk-use" class="primary">Use these selectors</button></div>';
    var els = panel.querySelectorAll('.rspk-slot');
    for (var i = 0; i < els.length; i++) {
      els[i].addEventListener('click', function () {
        state.armed = this.getAttribute('data-role');
        render();
      });
    }
    panel.querySelector('#rspk-smart').addEventListener('change', function () { state.smart = this.checked; });
    panel.querySelector('#rspk-auto').addEventListener('click', autoFill);
    panel.querySelector('#rspk-test').addEventListener('click', function () { test(false); });
    panel.querySelector('#rspk-use').addEventListener('click', useSelectors);
  }
  function result(html) {
    var b = panel.querySelector('#rspk-result');
    if (b) b.innerHTML = html;
  }

  // ---------- precision highlight (DevTools-style overlay) ----------
  // Hit-test the exact element under the cursor and draw a positioned box
  // with a tag label — same technique as browser inspectors and uBlock's
  // element picker. ArrowUp/Down (or Parent/Child buttons, or the crumb
  // trail) walk the tree; click or Enter assigns the highlighted element.
  var overlay = null, chip = null, current = null, childStack = [];
  function doc() { return frame.contentDocument; }
  function ensureOverlay(d) {
    overlay = d.createElement('div');
    overlay.id = 'rspk-box';
    overlay.style.cssText = 'position:fixed;display:none;border:2px solid #2563eb;' +
      'background:rgba(37,99,235,.12);pointer-events:none;z-index:2147483646;margin:0;padding:0;';
    chip = d.createElement('div');
    chip.id = 'rspk-chip';
    chip.style.cssText = 'position:fixed;display:none;background:#111827;color:#e5e7eb;' +
      'font:11px monospace;padding:2px 6px;border-radius:4px;pointer-events:none;z-index:2147483647;';
    d.body.appendChild(overlay);
    d.body.appendChild(chip);
  }
  function showBox(el) {
    var d = doc();
    if (!el || el === d.body || el === d.documentElement) { hideBox(); return; }
    current = el;
    var r = el.getBoundingClientRect();
    overlay.style.display = 'block';
    overlay.style.left = r.left + 'px';
    overlay.style.top = r.top + 'px';
    overlay.style.width = r.width + 'px';
    overlay.style.height = r.height + 'px';
    var label = selfSel(el) + ' · ' + Math.round(r.width) + '×' + Math.round(r.height);
    chip.style.display = 'block';
    chip.textContent = label;
    var cy = r.top - 22;
    if (cy < 0) cy = r.bottom + 2;
    chip.style.top = cy + 'px';
    chip.style.left = Math.max(0, r.left) + 'px';
    paintCrumb(el);
  }
  function hideBox() {
    current = null;
    if (overlay) overlay.style.display = 'none';
    if (chip) chip.style.display = 'none';
    if (crumbBar) crumbBar.textContent = 'hover the page…';
  }
  function paintCrumb(el) {
    var parts = crumb(el);
    crumbBar.innerHTML = 'body › ' + parts.map(function (p, i) {
      return '<span data-crumb="' + i + '">' + esc(p.label) + '</span>';
    }).join(' › ');
    var spans = crumbBar.querySelectorAll('span[data-crumb]');
    for (var j = 0; j < spans.length; j++) {
      spans[j].addEventListener('click', function (ev) {
        ev.stopPropagation();
        childStack = [];
        showBox(parts[parseInt(this.getAttribute('data-crumb'), 10)].el);
      });
    }
  }
  function goWider() {
    if (!current) return;
    var p = current.parentElement;
    var d = doc();
    if (!p || p === d.body || p === d.documentElement) return;
    childStack.push(current);
    showBox(p);
  }
  function goNarrower() {
    if (!childStack.length) return;
    showBox(childStack.pop());
  }
  // Pierce stretched overlay links: an empty anchor stretched over a whole
  // card would otherwise win every hit-test with a card-sized box. Look
  // through such anchors at the real content beneath (uBlock-style).
  function isPierceable(el) {
    if (!el || el.tagName !== 'A') return false;
    if (!el.getAttribute('href')) return false;
    if ((el.textContent || '').trim() !== '') return false;
    if (el.querySelector('img')) return false;
    return true;
  }
  function deepHit(d, x, y) {
    var hidden = [], el = null;
    try {
      for (var i = 0; i < 5; i++) {
        try { el = d.elementFromPoint(x, y); } catch (err) { el = null; break; }
        if (!el || !isPierceable(el)) break;
        el.style.display = 'none';
        hidden.push(el);
      }
    } finally {
      for (var j = 0; j < hidden.length; j++) hidden[j].style.display = '';
    }
    if (!el || el === d.body || el === d.documentElement) return null;
    return el;
  }
  function onMove(e) {
    var d = doc();
    var el = deepHit(d, e.clientX, e.clientY);
    if (!el) { hideBox(); return; }
    if (el === current) return;
    childStack = [];
    showBox(el);
  }
  function onLeave() { hideBox(); }
  function onScroll() { if (current) showBox(current); }
  function onClick(e) {
    e.preventDefault();
    e.stopPropagation();
    var d = doc();
    var el = current;
    if (!el || !d.contains(el)) {
      el = deepHit(d, e.clientX, e.clientY) || e.target;
    }
    assign(el, false);
  }
  function onSubmit(e) { e.preventDefault(); e.stopPropagation(); }
  function onKey(e) {
    if (document.getElementById('pickerModal').hidden) return;
    if (e.key === 'ArrowUp') { e.preventDefault(); goWider(); }
    else if (e.key === 'ArrowDown') { e.preventDefault(); goNarrower(); }
    else if (e.key === 'Enter') {
      // don't hijack Enter in panel inputs/buttons
      var t = e.target;
      if (t && (t.tagName === 'INPUT' || t.tagName === 'BUTTON' || t.tagName === 'A')) return;
      if (current) { e.preventDefault(); assign(current, false); }
    } else if (e.key === 'Escape') { closePicker(); }
  }

  function assign(el, exact) {
    var d = doc();
    if (!el || el === d.body || el === d.documentElement) return;
    if (state.armed === 'item') {
      state.item = selectorFor(el, !exact && state.smart, d);
      state.armed = 'title';
      if (!state.linkTouched) {
        var sl = stretchedLinkSel(d, state.item);
        if (sl) state.link = sl;
      }
    } else if (state.armed === 'title') {
      state.title = state.item ? relativeSel(state.item, el, d) : selfSel(el);
      state.armed = 'link';
      if (!state.linkTouched && state.item) {
        var sl2 = stretchedLinkSel(d, state.item);
        if (sl2) state.link = sl2;
      }
    } else {
      state.link = state.item ? relativeSel(state.item, el, d) : selfSel(el);
      state.linkTouched = true;
      state.armed = 'item';
    }
    render();
    test(true);
  }

  // ---------- server ----------
  function test(quiet) {
    if (!state.item) { if (!quiet) result('Pick an item container first.'); return; }
    if (!quiet) result('Testing…');
    fetch('/api/preview', { method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ url: pageUrl, item_selector: state.item, title_selector: state.title, link_selector: state.link }) })
      .then(function (r) { return r.json().then(function (j) { return { ok: r.ok, j: j }; }); })
      .then(function (res) {
        if (!res.ok) { result('Error: ' + esc(res.j.error || 'preview failed')); return; }
        var items = res.j || [];
        if (!items.length) { result('Matches 0 items — turn Smart-container off, or pick a parent from the breadcrumb.'); return; }
        render('Matches ' + items.length + ' sample item(s) with <code>' + esc(state.item) + '</code>:', items.slice(0, 3));
      })
      .catch(function (e) { result('Error: ' + esc(e.message)); });
  }
  function autoFill() {
    result('Detecting…');
    fetch('/api/detect', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ url: pageUrl }) })
      .then(function (r) { return r.json().then(function (j) { return { ok: r.ok, j: j }; }); })
      .then(function (res) {
        if (!res.ok || !res.j.item_selector) { render('Auto-detect found nothing usable — pick manually. ' + esc((res.j && res.j.error) || '')); return; }
        state.item = res.j.item_selector;
        state.title = res.j.title_selector || 'a';
        state.link = res.j.link_selector || 'a';
        state.armed = 'item';
        test(true);
      })
      .catch(function (e) { render('Error: ' + esc(e.message)); });
  }
  function useSelectors() {
    if (!state.item) { result('Pick an item container first.'); return; }
    var form = document.getElementById('siteForm');
    if (form.elements.item_selector) form.elements.item_selector.value = state.item;
    if (form.elements.title_selector) form.elements.title_selector.value = state.title;
    if (form.elements.link_selector) form.elements.link_selector.value = state.link;
    var m = form.querySelector('input[name=extraction_mode][value=manual]');
    if (m) m.checked = true;
    document.getElementById('manualDetails').open = true;
    closePicker();
  }

  // ---------- diagnostics ----------
  function diag(err) {
    return (err && (err.name + ': ' + err.message)) || String(err);
  }

  // ---------- open / close ----------
  // Hook as soon as the document is interactive instead of waiting for the
  // load event (slow/blocked subresources like tracking fonts delay it).
  // Guarded so load + poll can't double-attach.
  function hookFrame() {
    var d;
    try { d = doc(); } catch (e) { result('Cannot read preview (' + esc(diag(e)) + '). The preview failed to load — check the page URL, then close and retry.'); return; }
    if (!d || !d.body) { result('Preview is empty — the page may need JavaScript to render.'); return; }
    if (d.__rspkHooked) return;
    d.__rspkHooked = true;
    ensureOverlay(d);
    d.addEventListener('mousemove', onMove);
    d.addEventListener('mouseleave', onLeave);
    d.addEventListener('click', onClick, true);
    d.addEventListener('submit', onSubmit, true);
    d.addEventListener('scroll', onScroll, true);
    render();
  }
  function onFrameLoad() { hookFrame(); }
  window.openPicker = function (url) {
    pageUrl = url;
    state = { armed: 'item', smart: true, item: '', title: 'a', link: 'a', linkTouched: false };
    current = null; childStack = []; overlay = null; chip = null;
    frame = document.getElementById('pickFrame');
    crumbBar = document.getElementById('pickerCrumb');
    panel = document.getElementById('rspk-panel');
    crumbBar.textContent = 'loading preview…';
    render('Loading preview…');
    document.getElementById('pickerModal').hidden = false;
    if (!window._rspkKeys) {
      window._rspkKeys = true;
      window.addEventListener('keydown', onKey);
    }
    var bw = document.getElementById('pickWider');
    var bn = document.getElementById('pickNarrower');
    if (bw && !bw._rspk) { bw._rspk = true; bw.addEventListener('click', goWider); }
    if (bn && !bn._rspk) { bn._rspk = true; bn.addEventListener('click', goNarrower); }
    frame.onload = onFrameLoad;
    frame.src = '/pick?url=' + encodeURIComponent(url);
    // Poll for interactive state: subresources (fonts/trackers) may delay
    // the load event by a minute or never resolve when blocked.
    var tries = 0;
    var timer = setInterval(function () {
      tries++;
      var dd = null, blocked = false;
      try { dd = frame.contentDocument; } catch (e) { blocked = true; }
      if (blocked) {
        clearInterval(timer);
        result('Cannot read preview. The preview failed to load — check the page URL, then close and retry.');
      } else if (dd && (dd.readyState === 'interactive' || dd.readyState === 'complete') && dd.body) {
        clearInterval(timer);
        hookFrame();
      } else if (tries > 100) {
        clearInterval(timer);
      }
    }, 300);
  };
  window.closePicker = function () {
    try { if (frame) frame.src = 'about:blank'; } catch (e) {}
    current = null; childStack = [];
    document.getElementById('pickerModal').hidden = true;
  };
  // Test hook: pure selector helpers reachable under node with a DOM stub.
  window._rspkTest = { stableToken: stableToken, selfSel: selfSel, sig: sig,
    siblingsSame: siblingsSame, smartContainer: smartContainer,
    selectorFor: selectorFor, crumb: crumb, relativeSel: relativeSel,
    stretchedLinkSel: stretchedLinkSel, isPierceable: isPierceable };
  // Debug hook: live picker state for scripted verification.
  window._rspkState = function () {
    return { hasOverlay: !!(overlay && overlay.isConnected), hasCurrent: !!current,
      item: state.item, title: state.title, link: state.link, armed: state.armed };
  };
})();
