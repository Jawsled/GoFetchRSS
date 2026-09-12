const body = document.getElementById('sitesBody');
const errBox = document.getElementById('error');
const modal = document.getElementById('modal');
const form = document.getElementById('siteForm');
const modalTitle = document.getElementById('modalTitle');
let editingId = null;

function showErr(m){ errBox.textContent = m; errBox.hidden = !m; }
function feedUrl(id, ext){ return `${location.origin}/feeds/${id}.${ext}`; }
function mode(){ const el = form.querySelector('input[name=extraction_mode]:checked'); return el ? el.value : 'auto'; }
function kind(){ const el = form.querySelector('input[name=source_kind]:checked'); return el ? el.value : 'html'; }
function toggleKind(){
  const k = kind();
  document.getElementById('htmlFields').hidden = (k !== 'html');
  document.getElementById('jsonFields').hidden = (k !== 'json');
  document.getElementById('weiboFields').hidden = (k !== 'weibo');
}
form.querySelectorAll('input[name=source_kind]').forEach(el=>{ el.addEventListener('change', toggleKind); });

async function load(){
  showErr('');
  try{
    const r = await fetch('/api/sites');
    const sites = await r.json();
    if(!sites.length){ body.innerHTML = '<tr><td colspan="7">No sites yet. Click “+ Add site” — just paste a URL, auto-detect does the rest.</td></tr>'; return; }
    body.innerHTML = '';
    for(const s of sites){
      const tr = document.createElement('tr');
      let status;
      if(s.needs_review){
        status = `<span class="status-err">⚠ Needs review — found 0 items 3+ times (site may have changed). Edit selectors.</span>`;
      } else if(s.last_error){
        status = `<span class="status-err">Error: ${escapeHtml(s.last_error).slice(0,120)}</span>`;
      } else {
        status = s.last_checked ? `<span class="status-ok">OK ${escapeHtml(s.last_checked)}</span>` : 'never checked';
      }
      const modeBadge = escapeHtml(s.source_kind && s.source_kind !== 'html' ? s.source_kind : (s.extraction_mode || 'manual'));
      const nativeFeed = s.discovered_feed ? `<br><small>Native feed: <a href="${escapeHtml(s.discovered_feed)}" target="_blank" style="color:#2563eb">use directly in reader</a></small>` : '';
      tr.innerHTML = `
        <td><b>${escapeHtml(s.name)}</b><br><small>${s.enabled?'enabled':'disabled'} · ${modeBadge}</small></td>
        <td><a href="${escapeHtml(s.url)}" target="_blank" style="color:#2563eb">${escapeHtml(s.url)}</a>${nativeFeed}</td>
        <td>${s.poll_minutes}m</td>
        <td>${status}</td>
        <td>${s.item_count||0}</td>
        <td><div class="feed-links">
          ${feedRow('RSS', feedUrl(s.id,'xml'))}
          ${feedRow('Atom', feedUrl(s.id,'atom'))}
          ${feedRow('JSON', feedUrl(s.id,'json'))}
        </div></td>
        <td><div class="rowbtns">
          <button data-refresh="${s.id}">Refresh</button>
          <button data-edit="${s.id}">Edit</button>
          <button data-del="${s.id}">Del</button>
        </div></td>`;
      body.appendChild(tr);
    }
  }catch(e){ showErr('Failed to load sites: '+e.message); }
}

function escapeHtml(s){ return String(s??'').replace(/[&<>"']/g, c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c])); }

const COPY_SVG = '<svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.6"><rect x="5" y="5" width="9" height="9" rx="1.5"/><path d="M11 5V4a1.5 1.5 0 0 0-1.5-1.5H4A1.5 1.5 0 0 0 2.5 4v5.5A1.5 1.5 0 0 0 4 11h1"/></svg>';
const CHECK_SVG = '<svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="2"><path d="M2.5 8.5l3.5 3.5 7-8"/></svg>';

function feedRow(label, url){
  return `<div class="feed-row"><span class="feed-label">${label}</span>`+
    `<input class="feed-url" readonly value="${escapeHtml(url)}" onclick="this.select()">`+
    `<button class="icon-btn" data-copy="${escapeHtml(url)}" title="Copy ${label} URL — paste it into your reader">${COPY_SVG}</button></div>`;
}

async function copyText(text){
  try{
    await navigator.clipboard.writeText(text);
    return true;
  }catch(e){
    // fallback for non-secure contexts / older browsers
    const ta = document.createElement('textarea');
    ta.value = text;
    ta.style.position = 'fixed';
    ta.style.opacity = '0';
    document.body.appendChild(ta);
    ta.select();
    let ok = false;
    try{ ok = document.execCommand('copy'); }catch(_){ ok = false; }
    ta.remove();
    return ok;
  }
}

body.addEventListener('click', async (e)=>{
  const cpBtn = e.target.closest('[data-copy]');
  if(cpBtn){
    const ok = await copyText(cpBtn.dataset.copy);
    cpBtn.innerHTML = ok ? CHECK_SVG : '!';
    cpBtn.classList.add('copied');
    cpBtn.title = ok ? 'Copied! Paste it into your reader.' : 'Copy failed — select the URL manually';
    setTimeout(()=>{ cpBtn.innerHTML = COPY_SVG; cpBtn.classList.remove('copied'); }, 1500);
    return;
  }
  const rf = e.target.dataset.refresh;
  if(rf){ await fetch(`/api/sites/${rf}/refresh`,{method:'POST'}); showErr('Refresh started — reload in a few seconds.'); setTimeout(load,4000); return; }
  const del = e.target.dataset.del;
  if(del && confirm('Delete this site and its items?')){ await fetch(`/api/sites/${del}`,{method:'DELETE'}); load(); return; }
  const ed = e.target.dataset.edit;
  if(ed){
    const r = await fetch('/api/sites'); const sites = await r.json();
    const s = sites.find(x=>x.id===ed); if(!s) return;
    editingId = ed; modalTitle.textContent='Edit site';
    for(const k of ['name','url','item_selector','title_selector','link_selector','content_selector','date_selector','poll_minutes','api_url','items_path','item_root','title_path','content_path','link_path','link_template','date_path','date_format','headers','weibo_uid']){
      if(form.elements[k]) form.elements[k].value = s[k] ?? '';
    }
    form.elements.enabled.checked = !!s.enabled;
    const m = s.extraction_mode || 'manual';
    form.querySelector(`input[name=extraction_mode][value=${m}]`).checked = true;
    const k = s.source_kind || 'html';
    form.querySelector(`input[name=source_kind][value=${k}]`).checked = true;
    toggleKind();
    document.getElementById('manualDetails').open = (m === 'manual' && k === 'html');
    document.getElementById('detectResult').hidden = true;
    document.getElementById('preview').hidden = true;
    modal.hidden = false; return;
  }
});

document.getElementById('addBtn').onclick = ()=>{
  editingId=null; modalTitle.textContent='Add site'; form.reset();
  form.querySelector('input[name=extraction_mode][value=auto]').checked = true;
  form.querySelector('input[name=source_kind][value=html]').checked = true;
  toggleKind();
  form.elements.poll_minutes.value=60; form.elements.enabled.checked=true;
  document.getElementById('manualDetails').open = false;
  document.getElementById('detectResult').hidden=true;
  document.getElementById('preview').hidden=true; modal.hidden=false;
};
document.getElementById('cancelBtn').onclick = ()=>{ modal.hidden=true; };

form.querySelectorAll('input[name=extraction_mode]').forEach(el=>{
  el.addEventListener('change', ()=>{
    document.getElementById('manualDetails').open = (mode() === 'manual');
  });
});

function renderGuess(box, g){
  const items = (g.preview||[]).map(x=>`<li><a href="${escapeHtml(x.URL)}" target="_blank">${escapeHtml(x.Title)}</a></li>`).join('');
  const feed = g.feed_url ? `<br>ℹ️ This site already publishes a native feed: <a href="${escapeHtml(g.feed_url)}" target="_blank">${escapeHtml(g.feed_url)}</a> — you can paste that into your reader directly.` : '';
  box.innerHTML = `<b>Auto-detect (${escapeHtml(g.confidence)} confidence, ${g.sample_count} items):</b> `+
    `<code>${escapeHtml(g.item_selector)}</code><br><small>${escapeHtml(g.reason||'')}</small>${feed}`+
    `<ul>${items}</ul>`;
  const cands = g.candidates || [];
  if(cands.length){
    box.innerHTML += `<b>Not the right list? Pick one of these instead:</b>` +
      cands.map((c,i)=>{
        const s = (c.samples||[]).map(x=>escapeHtml(x.Title)).join(' · ');
        return `<div class="cand"><code>${escapeHtml(c.item_selector)}</code> <small>(${c.count} items: ${s})</small> `+
          `<button data-usecand="${i}">Use this</button></div>`;
      }).join('');
    box._candidates = cands;
  }
}

document.addEventListener('click', (e)=>{
  const u = e.target.dataset && e.target.dataset.usecand;
  if(u === undefined) return;
  const box = document.getElementById('detectResult');
  const c = (box._candidates || [])[parseInt(u,10)];
  if(!c) return;
  for(const k of ['item_selector','title_selector','link_selector']){ if(form.elements[k]) form.elements[k].value = c[k] || ''; }
  document.getElementById('manualDetails').open = true;
  showErr(`Using "${c.item_selector}" — press Test selectors to verify, then Save.`);
});

document.getElementById('detectBtn').onclick = async ()=>{
  const url = form.elements.url.value.trim();
  if(!url){ showErr('Enter a Page URL first.'); return; }
  const box = document.getElementById('detectResult');
  box.hidden=false; box.textContent='Detecting…';
  try{
    const r = await fetch('/api/detect',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({url})});
    const g = await r.json();
    if(!r.ok){ box.textContent='Error: '+(g.error||r.status); return; }
    renderGuess(box, g);
    // fill selectors so Save works even if server-side re-detect hiccups; user can edit
    for(const k of ['item_selector','title_selector','link_selector','content_selector','date_selector']){
      if(form.elements[k] && g[k]) form.elements[k].value = g[k];
    }
  }catch(e){ box.textContent='Error: '+e.message; }
};

document.getElementById('pickBtn').onclick = ()=>{
  const url = form.elements.url.value.trim();
  if(!url){ showErr('Enter a Page URL first, then Pick visually.'); return; }
  openPicker(url);
};
document.getElementById('pickerClose').onclick = ()=>{ closePicker(); };

document.getElementById('testBtn').onclick = async ()=>{
  const data = Object.fromEntries(new FormData(form).entries());
  data.poll_minutes = parseInt(data.poll_minutes||'60',10);
  data.source_kind = kind();
  const box = document.getElementById('preview');
  box.hidden=false; box.textContent='Testing…';
  try{
    const r = await fetch('/api/preview',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(data)});
    const j = await r.json();
    if(!r.ok){ box.textContent='Error: '+(j.error||r.status); return; }
    if(!j || !j.length){ box.textContent='No items matched. Check the mappings/selectors.'; return; }
    box.innerHTML = `<b>${j.length} sample item(s):</b><ul>`+j.map(x=>`<li><a href="${escapeHtml(x.URL)}" target="_blank">${escapeHtml(x.Title)}</a></li>`).join('')+`</ul>`;
  }catch(e){ box.textContent='Error: '+e.message; }
};

form.onsubmit = async (e)=>{
  e.preventDefault();
  const data = Object.fromEntries(new FormData(form).entries());
  data.enabled = form.elements.enabled.checked;
  data.poll_minutes = parseInt(data.poll_minutes||'60',10);
  data.extraction_mode = mode();
  if(data.extraction_mode === 'auto'){
    // let server run detection; drop stale manual values unless user opened them
    if(!document.getElementById('manualDetails').open){
      for(const k of ['item_selector','title_selector','link_selector','content_selector','date_selector']) delete data[k];
    }
  }
  const url = editingId ? `/api/sites/${editingId}` : '/api/sites';
  const method = editingId ? 'PUT' : 'POST';
  const r = await fetch(url,{method,headers:{'Content-Type':'application/json'},body:JSON.stringify(data)});
  const j = await r.json().catch(()=>({}));
  if(!r.ok){
    if(j.guess){ renderGuess(document.getElementById('preview'), j.guess); document.getElementById('preview').hidden=false; }
    showErr('Save failed: '+(j.error||r.status)); return;
  }
  modal.hidden=true; load();
};

load();
setInterval(load, 30000);
