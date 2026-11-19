/* qbview 前端：原生 JS，无框架、无构建。
   数据只从自己的 /api/state 来，不直接碰 qBittorrent。 */

const POLL_MS = 2000;

const el = (id) => document.getElementById(id);

const state = {
  filter: 'all',
  torrents: [],
  timer: null,
  inflight: false,
  lastOk: 0,
  failed: 0,
};

/* 顶栏标题跟筛选走，不重复写应用名（左栏品牌区已有） */
const FILTER_NAMES = { all: '全部', down: '下载中', seed: '做种', pause: '已暂停', error: '异常' };

/* ---------- 主题 ---------- */

function applyTheme(name) {
  document.documentElement.dataset.theme = name;
  document.documentElement.style.colorScheme = name;
  try { localStorage.setItem('qbview-theme', name); } catch (_) { /* 隐私模式忽略 */ }
}

function initTheme() {
  let saved = '';
  try { saved = localStorage.getItem('qbview-theme') || ''; } catch (_) { }
  if (saved === 'dark' || saved === 'light') { applyTheme(saved); return; }
  const dark = window.matchMedia('(prefers-color-scheme: dark)');
  applyTheme(dark.matches ? 'dark' : 'light');
}

/* ---------- 格式化 ---------- */

const UNITS = ['B', 'KB', 'MB', 'GB', 'TB', 'PB'];

function fmtBytes(n) {
  if (!n || n < 0) return '0 B';
  let i = 0;
  let v = n;
  while (v >= 1024 && i < UNITS.length - 1) { v /= 1024; i++; }
  const d = v >= 100 ? 0 : v >= 10 ? 1 : 2;
  return v.toFixed(d) + ' ' + UNITS[i];
}

function fmtSpeed(n) {
  if (!n || n <= 0) return '0 B/s';
  return fmtBytes(n) + '/s';
}

function fmtEta(sec) {
  if (!sec || sec <= 0 || sec >= 8640000) return '—';
  if (sec < 60) return sec + ' 秒';
  if (sec < 3600) return Math.round(sec / 60) + ' 分';
  if (sec < 86400) {
    const h = Math.floor(sec / 3600);
    const m = Math.round((sec % 3600) / 60);
    return m ? h + ' 时 ' + m + ' 分' : h + ' 时';
  }
  return Math.round(sec / 86400) + ' 天';
}

const esc = (s) => String(s).replace(/[&<>"']/g, (c) => ({
  '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
}[c]));

/* ---------- 渲染 ---------- */

function matches(t, filter) {
  if (filter === 'all') return true;
  if (filter === 'pause') return t.kind === 'pause' || t.kind === 'other';
  return t.kind === filter;
}

function render() {
  const list = el('list');
  const empty = el('empty');

  const items = state.torrents.filter((t) => matches(t, state.filter));

  // 下载中优先，其次按已添加时间倒序
  items.sort((a, b) => {
    const order = { down: 0, check: 1, seed: 2, error: 3, pause: 4, other: 5 };
    const d = (order[a.kind] ?? 9) - (order[b.kind] ?? 9);
    return d !== 0 ? d : (b.addedOn - a.addedOn);
  });

  el('listHint').textContent = items.length + ' 个';
  el('pageTitle').textContent = FILTER_NAMES[state.filter] || '全部';

  if (!items.length) {
    list.innerHTML = '';
    empty.hidden = false;
    empty.querySelector('p').textContent =
      state.filter === 'all' ? '这里还没有任务' : '该分类下没有任务';
    empty.querySelector('.sub').textContent =
      state.filter === 'all' ? '在 qBittorrent 里添加种子，列表会自动出现' : '换个分类看看';
    return;
  }
  empty.hidden = true;

  // 整体重绘会杀掉进度条的过渡动画，所以用 key 复用已有节点。
  const existing = new Map();
  for (const node of list.children) existing.set(node.dataset.hash, node);

  const frag = document.createDocumentFragment();
  for (const t of items) {
    let node = existing.get(t.hash);
    if (!node) {
      node = document.createElement('article');
      node.dataset.hash = t.hash;
      node.innerHTML = rowSkeleton();
    }
    existing.delete(t.hash);
    fillRow(node, t);
    frag.appendChild(node);
  }
  for (const gone of existing.values()) gone.remove();
  list.appendChild(frag);
}

function rowSkeleton() {
  return `
    <div class="row-name"></div>
    <div class="row-pct"></div>
    <div class="pbar"><i></i></div>
    <div class="row-meta"></div>
    <div class="row-acts"></div>`;
}

const ICON_PAUSE = '<svg viewBox="0 0 24 24" width="12" height="12" fill="currentColor"><rect x="6" y="4" width="4" height="16" rx="1"/><rect x="14" y="4" width="4" height="16" rx="1"/></svg>';
const ICON_PLAY = '<svg viewBox="0 0 24 24" width="12" height="12" fill="currentColor"><path d="M8 5v14l11-7z"/></svg>';

function fillRow(node, t) {
  node.className = 'row k-' + t.kind;

  const pct = Math.max(0, Math.min(100, t.progress * 100));
  const done = t.progress >= 0.9999;

  const name = node.querySelector('.row-name');
  name.textContent = t.name;
  name.title = t.name;

  const pctEl = node.querySelector('.row-pct');
  pctEl.textContent = done ? '完成' : pct.toFixed(1) + '%';
  pctEl.classList.toggle('done', done);

  node.querySelector('.pbar > i').style.width = pct.toFixed(2) + '%';

  const parts = [];
  parts.push(`<span class="state-tag ${t.kind}">${esc(t.stateText)}</span>`);
  if (t.kind === 'down') {
    if (t.dlSpeed > 0) parts.push(`<span class="sp spd-down">↓ ${fmtSpeed(t.dlSpeed)}</span>`);
    if (t.upSpeed > 0) parts.push(`<span class="sp spd-up">↑ ${fmtSpeed(t.upSpeed)}</span>`);
    if (t.left > 0) parts.push(`<span class="sp"><span class="k">剩</span> ${fmtBytes(t.left)}</span>`);
    // qB 对未知 ETA 会给 8640000，直接渲染成「约 —」很难看
    if (t.eta > 0 && t.eta < 8640000) parts.push(`<span class="sp"><span class="k">约</span> ${fmtEta(t.eta)}</span>`);
  } else if (t.kind === 'seed') {
    if (t.upSpeed > 0) parts.push(`<span class="sp spd-up">↑ ${fmtSpeed(t.upSpeed)}</span>`);
    parts.push(`<span class="sp"><span class="k">分享率</span> ${(t.ratio || 0).toFixed(2)}</span>`);
  } else {
    if (t.dlSpeed > 0) parts.push(`<span class="sp spd-down">↓ ${fmtSpeed(t.dlSpeed)}</span>`);
    if (t.upSpeed > 0) parts.push(`<span class="sp spd-up">↑ ${fmtSpeed(t.upSpeed)}</span>`);
  }
  parts.push(`<span class="sp"><span class="k">大小</span> ${fmtBytes(t.size)}</span>`);
  if (t.seeds || t.peers) {
    parts.push(`<span class="sp"><span class="k">种</span> ${t.seeds} · <span class="k">连</span> ${t.peers}</span>`);
  }
  node.querySelector('.row-meta').innerHTML = parts.join('');

  // 删除不用原生 confirm（三连弹和卡片风格割裂），
  // 而是把按钮行原地换成三个明确选项；轮询重绘时 dataset.confirm 保留确认态。
  const acts = node.querySelector('.row-acts');
  if (node.dataset.confirm === '1') {
    acts.innerHTML = `
      <button class="btn tiny ghost" data-act="cancel-del">取消</button>
      <button class="btn tiny" data-act="del-task">仅移除</button>
      <button class="btn tiny danger-solid" data-act="del-files">连文件删</button>`;
  } else {
    const paused = t.kind === 'pause';
    acts.innerHTML = `
      <button class="btn tiny" data-act="toggle" data-mode="${paused ? 'resume' : 'pause'}">
        ${paused ? ICON_PLAY + '继续' : ICON_PAUSE + '暂停'}
      </button>
      <button class="btn tiny danger" data-act="delete">删除</button>`;
  }
}

function renderGlobal(g) {
  el('gDl').textContent = fmtSpeed(g.dlSpeed);
  el('gUp').textContent = fmtSpeed(g.upSpeed);
  el('cFree').textContent = g.free ? fmtBytes(g.free) : '—';
}

// 侧栏分类计数跟着数据走；四格里那颗"任务"是全量数
function renderCounts() {
  const c = { all: state.torrents.length, down: 0, seed: 0, pause: 0, error: 0 };
  for (const t of state.torrents) {
    if (t.kind === 'pause' || t.kind === 'other') c.pause++;
    else if (c[t.kind] !== undefined) c[t.kind]++;
  }
  el('cTotal').textContent = c.all;
  for (const node of document.querySelectorAll('#filters [data-count]')) {
    node.textContent = c[node.dataset.count] ?? 0;
  }
  updateTabsFade();
}

// 手机上分类标签横向滚动，最后一个常被屏幕边缘切半截；
// 只要还有内容没滚到就给行尾一个渐隐，暗示可以滑。
function updateTabsFade() {
  const nav = el('filters');
  const more = nav.scrollWidth > nav.clientWidth + 2 &&
               nav.scrollLeft + nav.clientWidth < nav.scrollWidth - 2;
  nav.classList.toggle('can-scroll', more);
}

function setConn(ok) {
  const pill = el('connPill');
  pill.className = 'pill ' + (ok ? 'ok' : 'bad');
  pill.textContent = ok ? '已连接' : '连接异常';
  pill.title = ok ? '' : '点右上角设置检查账号密码';
}

function showBanner(msg) {
  const b = el('banner');
  if (!msg) { b.hidden = true; return; }
  b.textContent = msg;
  b.hidden = false;
}

/* ---------- 数据 ---------- */

async function fetchState() {
  if (state.inflight) return;
  state.inflight = true;
  try {
    const r = await fetch('/api/state', { cache: 'no-store' });
    const d = await r.json();
    state.inflight = false;

    if (!d.ok) {
      setConn(false);
      if (d.configured === false) {
        openSheet();
        showBanner('尚未配置 qBittorrent，先填 WebUI 账号密码。');
      } else {
        showBanner(d.error || '读取失败');
      }
      return;
    }

    state.lastOk = Date.now();
    state.failed = 0;
    state.torrents = d.torrents || [];
    renderGlobal(d.global || {});
    renderCounts();
    showBanner('');
    setConn(true);
    render();
  } catch (e) {
    state.inflight = false;
    state.failed++;
    setConn(false);
    // 偶发网络抖动不必吼，连续两次才算真的断了
    if (state.failed >= 2) showBanner('无法连接到本机服务：' + e.message);
  }
}

function startPolling() {
  stopPolling();
  fetchState();
  state.timer = setInterval(fetchState, POLL_MS);
}

function stopPolling() {
  if (state.timer) { clearInterval(state.timer); state.timer = null; }
}

/* ---------- 操作 ---------- */

async function torrentAction(hash, act, withFiles) {
  const url = `/api/t/${encodeURIComponent(hash)}/${act}` + (withFiles ? '?files=1' : '');
  try {
    const r = await fetch(url, { method: 'POST' });
    const d = await r.json().catch(() => ({}));
    if (!r.ok || d.ok === false) throw new Error(d.error || ('HTTP ' + r.status));
  } catch (e) {
    showBanner('操作失败：' + e.message);
  }
  // 立刻拉一次，别等下一个轮询周期，否则按钮看起来没反应
  fetchState();
}

async function globalAction(act) {
  try {
    const r = await fetch('/api/global/' + act, { method: 'POST' });
    const d = await r.json().catch(() => ({}));
    if (!r.ok || d.ok === false) throw new Error(d.error || ('HTTP ' + r.status));
  } catch (e) {
    showBanner('操作失败：' + e.message);
  }
  fetchState();
}

/* ---------- 设置面板 ---------- */

function openSheet() {
  el('sheet').hidden = false;
  el('setupErr').hidden = true;
  loadConfigIntoSheet();
}

function closeSheet() {
  el('sheet').hidden = true;
}

// 端口来源的提示文案
function setPortHint(source, port, ports) {
  const hint = el('portHint');
  hint.classList.remove('warn');
  if (!port) {
    hint.textContent = '没找到 qBittorrent，可以手动填端口';
    hint.classList.add('warn');
    return;
  }
  const extra = (ports && ports.length > 1) ? `（共找到 ${ports.length} 个端口，已用第一个）` : '';
  const text = {
    auto: `已自动找到 qBittorrent，端口 ${port}${extra}`,
    'auto-saved': `当前端口 ${port}（之前自动探测到的）`,
    manual: `当前端口 ${port}（手动设置）`,
    saved: `当前端口 ${port}（手动设置）`,
    default: `当前用默认端口 ${port}，不对就改`,
    env: `当前端口 ${port}（来自环境变量）`,
  };
  hint.textContent = text[source] || `当前端口 ${port}`;
}

async function probePort() {
  const btn = el('btnProbe');
  const hint = el('portHint');
  btn.disabled = true;
  btn.textContent = '探测中…';
  hint.classList.remove('warn');
  hint.textContent = '正在扫描本机端口…';
  try {
    const r = await fetch('/api/probe', { method: 'POST' });
    const d = await r.json();
    if (d.ok && d.port) {
      el('inPort').value = d.port;
      setPortHint('auto', d.port, d.ports);
    } else {
      el('inPort').value = '';
      hint.textContent = d.error || '没找到，手动填端口吧';
      hint.classList.add('warn');
    }
  } catch (e) {
    hint.textContent = '探测失败：' + e.message;
    hint.classList.add('warn');
  } finally {
    btn.disabled = false;
    btn.textContent = '自动探测';
  }
}

async function loadConfigIntoSheet() {
  try {
    const r = await fetch('/api/config', { cache: 'no-store' });
    const d = await r.json();
    if (!el('inUser').value) el('inUser').value = d.user || '';
    el('inPass').placeholder = d.hasPass ? '留空则沿用已保存的密码' : '请输入密码';
    if (d.port > 0) {
      if (!el('inPort').value) el('inPort').value = d.port;
      setPortHint(d.portSource === 'auto' ? 'auto-saved' : d.portSource, d.port);
    } else {
      // 没有已知端口（比如自动探测没找到），面板里再试一次
      await probePort();
    }
  } catch (_) { /* 读取失败就让用户手填 */ }
}

async function saveSetup() {
  const btn = el('btnSave');
  const err = el('setupErr');
  err.hidden = true;
  btn.disabled = true;
  btn.textContent = '连接中…';

  try {
    const portRaw = parseInt(el('inPort').value, 10);
    const r = await fetch('/api/setup', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        user: el('inUser').value.trim(),
        pass: el('inPass').value,
        port: Number.isFinite(portRaw) ? portRaw : 0,
      }),
    });
    const d = await r.json().catch(() => ({}));
    if (!r.ok || d.ok === false) throw new Error(d.error || ('HTTP ' + r.status));
    el('inPass').value = '';
    closeSheet();
    showBanner('');
    startPolling();
  } catch (e) {
    err.textContent = e.message;
    err.hidden = false;
    // 端口类错误（假 qB / 网关占端口 / 端口不通）把提示落到端口输入框下面，
    // 别让用户只盯着红色错误条、不知道该改哪里。
    if (/端口|Web API|SID|网页/.test(e.message)) {
      const hint = el('portHint');
      hint.textContent = '这个端口连不上 qBittorrent，点「自动探测」重找，或手动填 WebUI 端口';
      hint.classList.add('warn');
    }
  } finally {
    btn.disabled = false;
    btn.textContent = '保存并连接';
  }
}

/* ---------- 事件绑定 ---------- */

el('filters').addEventListener('click', (e) => {
  const btn = e.target.closest('button[data-filter]');
  if (!btn) return;
  state.filter = btn.dataset.filter;
  for (const c of el('filters').querySelectorAll('button')) {
    c.classList.toggle('on', c === btn);
  }
  render();
});

el('list').addEventListener('click', (e) => {
  const btn = e.target.closest('[data-act]');
  if (!btn) return;
  const row = btn.closest('.row');
  if (!row) return;
  const hash = row.dataset.hash;
  const t = state.torrents.find((x) => x.hash === hash);
  if (!t) return;

  if (btn.dataset.act === 'toggle') {
    torrentAction(hash, btn.dataset.mode || 'pause', false);
    return;
  }
  // 删除：先原地展开三个选项，选择后才真正执行
  if (btn.dataset.act === 'delete') {
    row.dataset.confirm = '1';
    fillRow(row, t);
    return;
  }
  if (btn.dataset.act === 'cancel-del') {
    delete row.dataset.confirm;
    fillRow(row, t);
    return;
  }
  if (btn.dataset.act === 'del-task' || btn.dataset.act === 'del-files') {
    delete row.dataset.confirm;
    torrentAction(hash, 'delete', btn.dataset.act === 'del-files');
  }
});

el('btnPauseAll').addEventListener('click', () => globalAction('pause'));
el('btnResumeAll').addEventListener('click', () => globalAction('resume'));
el('btnSettings').addEventListener('click', openSheet);
el('btnSave').addEventListener('click', saveSetup);
el('btnProbe').addEventListener('click', probePort);
el('filters').addEventListener('scroll', updateTabsFade, { passive: true });
window.addEventListener('resize', updateTabsFade);
el('btnTheme').addEventListener('click', () => {
  applyTheme(document.documentElement.dataset.theme === 'dark' ? 'light' : 'dark');
});

for (const node of document.querySelectorAll('[data-close]')) {
  node.addEventListener('click', closeSheet);
}

// 切到后台就停轮询，手机省电；回到前台立刻刷新一次。
document.addEventListener('visibilitychange', () => {
  if (document.hidden) stopPolling();
  else startPolling();
});

/* ---------- 启动 ---------- */

initTheme();

(async function boot() {
  try {
    const r = await fetch('/api/config', { cache: 'no-store' });
    const d = await r.json();
    if (!d.configured) {
      el('inUser').value = d.user || '';
      openSheet();
      showBanner('尚未配置 qBittorrent，先填 WebUI 账号密码。');
      return;
    }
  } catch (_) { /* 落到下面正常启动 */ }
  startPolling();
})();

if ('serviceWorker' in navigator) {
  window.addEventListener('load', () => {
    navigator.serviceWorker.register('sw.js').catch(() => {});
  });
}
