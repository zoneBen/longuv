// iadu-sim embedded management console.
// No external dependencies; all state is refreshed through REST + SSE.

const ZONES = ['top', 'mid', 'bottom'];
const ZONE_LABEL = { top: '上部', mid: '中部', bottom: '下部' };
const ZONE_TAG = { top: 'TOP', mid: 'MID', bottom: 'BOT' };
const ZONE_COLOR = { top: '#4fd1c5', mid: '#93a3b5', bottom: '#f5b35c' };

const state = {
  summary: null,
  route: { name: 'overview' },
  renderedRoute: '',
  token: localStorage.getItem('iadu-sim-token') || '',
  sse: null,
  sseState: 'offline',
  device: null,
  deviceId: '',
  deviceViewToken: 0,
  history: new Map(),
  overviewSig: '',
  portsSig: '',
  sidebarSig: '',
  logs: [],
  logBuffer: [],
  logPaused: false,
  logReady: false,
  logFilter: { port: 'all', dir: 'all' },
  logAutoScroll: true,
};

/* ------------------------------------------------------------------ utils */

function esc(value) {
  return String(value === undefined || value === null ? '' : value).replace(/[&<>"']/g, (c) => ({
    '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
  }[c]));
}

function parseTime(value) {
  if (!value) return null;
  const s = String(value);
  const m = s.match(/^(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2})(\.\d+)?(.*)$/);
  if (!m) {
    const d = new Date(s);
    return Number.isNaN(d.getTime()) ? null : d;
  }
  const frac = m[2] ? m[2].slice(0, 4) : '';
  const d = new Date(m[1] + frac + m[3]);
  return Number.isNaN(d.getTime()) ? null : d;
}

function fmtClock(d) {
  if (!d) return '—';
  const p = (n, w = 2) => String(n).padStart(w, '0');
  return p(d.getHours()) + ':' + p(d.getMinutes()) + ':' + p(d.getSeconds()) + '.' + p(d.getMilliseconds(), 3);
}

function fmtDateTime(d) {
  if (!d) return '';
  const p = (n) => String(n).padStart(2, '0');
  return d.getFullYear() + '-' + p(d.getMonth() + 1) + '-' + p(d.getDate()) + ' ' +
    p(d.getHours()) + ':' + p(d.getMinutes()) + ':' + p(d.getSeconds());
}

function relTime(value) {
  const d = typeof value === 'string' || value instanceof Date ? parseTime(value) : null;
  if (!d) return '从未';
  const sec = Math.max(0, (Date.now() - d.getTime()) / 1000);
  if (sec < 5) return '刚刚';
  if (sec < 60) return Math.floor(sec) + ' 秒前';
  if (sec < 3600) return Math.floor(sec / 60) + ' 分钟前';
  if (sec < 86400) return Math.floor(sec / 3600) + ' 小时前';
  return Math.floor(sec / 86400) + ' 天前';
}

function fmtUptime(sec) {
  const s = Math.max(0, Number(sec) || 0);
  const d = Math.floor(s / 86400);
  const h = Math.floor((s % 86400) / 3600);
  const m = Math.floor((s % 3600) / 60);
  if (d > 0) return d + 'd ' + h + 'h';
  if (h > 0) return h + 'h ' + m + 'm';
  if (m > 0) return m + 'm ' + Math.floor(s % 60) + 's';
  return Math.floor(s) + 's';
}

function fmtNum(v, digits = 1) {
  const n = Number(v);
  return Number.isFinite(n) ? n.toFixed(digits) : '—';
}

function displayListen(addr) {
  if (!addr) return '—';
  return String(addr).replace(/^\[::\]:/, '0.0.0.0:').replace(/^\[::\]$/, '0.0.0.0');
}

function hex2(n) {
  return Number(n || 0).toString(16).toUpperCase().padStart(2, '0');
}

function hex4(n) {
  return (Number(n || 0) & 0xffff).toString(16).toUpperCase().padStart(4, '0');
}

function bitSet(mask, index) {
  const m = Number(mask) || 0;
  return Math.floor(m / Math.pow(2, index)) % 2 === 1;
}

function countBits(mask) {
  let count = 0;
  for (let i = 0; i < 42; i++) if (bitSet(mask, i)) count++;
  return count;
}

function qs(sel, root) {
  return (root || document).querySelector(sel);
}

function qsa(sel, root) {
  return Array.from((root || document).querySelectorAll(sel));
}

function toast(message, type = 'ok') {
  const root = document.getElementById('toast-root');
  if (!root) return;
  const el = document.createElement('div');
  el.className = 'toast ' + type;
  el.textContent = message;
  root.appendChild(el);
  setTimeout(() => el.remove(), 4200);
}

function reportError(err) {
  if (!err) return;
  if (err.status === 401) {
    openTokenModal();
    return;
  }
  toast(err.message || String(err), 'error');
}

async function copyText(text) {
  try {
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(text);
      toast('已复制');
      return;
    }
  } catch (err) {
    // fall through to the legacy path
  }
  const area = document.createElement('textarea');
  area.value = text;
  area.setAttribute('readonly', '');
  area.className = 'copy-helper';
  document.body.appendChild(area);
  area.select();
  try {
    document.execCommand('copy');
    toast('已复制');
  } catch (err) {
    toast('复制失败，请手动选择文本', 'error');
  }
  area.remove();
}

function debounce(fn, ms) {
  let timer = null;
  return function debounced() {
    clearTimeout(timer);
    timer = setTimeout(fn, ms);
  };
}

/* -------------------------------------------------------------------- api */

async function api(path, options = {}) {
  const opts = Object.assign({}, options);
  const headers = Object.assign({ Accept: 'application/json' }, opts.headers || {});
  if (opts.body !== undefined && typeof opts.body !== 'string') {
    headers['Content-Type'] = 'application/json';
    opts.body = JSON.stringify(opts.body);
  }
  if (state.token) headers.Authorization = 'Bearer ' + state.token;
  opts.headers = headers;
  const res = await fetch(path, opts);
  let payload = null;
  try {
    payload = await res.json();
  } catch (err) {
    payload = null;
  }
  if (res.status === 401) {
    openTokenModal();
    const e = new Error('需要有效的访问令牌');
    e.status = 401;
    throw e;
  }
  if (!res.ok || !payload || payload.ok === false) {
    const message = payload && payload.error && payload.error.message
      ? payload.error.message
      : '请求失败（HTTP ' + res.status + '）';
    const e = new Error(message);
    e.status = res.status;
    throw e;
  }
  return payload.data;
}

/* -------------------------------------------------------------------- sse */

function updateLinkStatus() {
  const box = document.getElementById('link-status');
  if (!box) return;
  const labels = { live: '实时', connecting: '连接中', reconnecting: '重连中', offline: '离线' };
  const classes = { live: 'live', connecting: 'reconnecting', reconnecting: 'reconnecting', offline: 'offline' };
  box.className = 'link-status ' + (classes[state.sseState] || 'offline');
  const dot = box.querySelector('.dot');
  if (dot) dot.className = 'dot ' + (state.sseState === 'live' ? 'online pulse' : state.sseState === 'reconnecting' ? 'manual' : 'idle');
  const text = box.querySelector('#link-text');
  if (text) text.textContent = labels[state.sseState] || '离线';
}

function connectSSE() {
  if (state.sse) {
    state.sse.close();
    state.sse = null;
  }
  const url = '/api/events' + (state.token ? '?token=' + encodeURIComponent(state.token) : '');
  const es = new EventSource(url);
  state.sse = es;
  state.sseState = 'connecting';
  updateLinkStatus();
  es.addEventListener('open', () => {
    state.sseState = 'live';
    updateLinkStatus();
  });
  es.addEventListener('snapshot', (ev) => {
    let data = null;
    try {
      data = JSON.parse(ev.data);
    } catch (err) {
      return;
    }
    applySummary(data, true);
  });
  es.addEventListener('frames', (ev) => {
    let data = null;
    try {
      data = JSON.parse(ev.data);
    } catch (err) {
      return;
    }
    if (data && Array.isArray(data.frames)) appendLogs(data.frames);
  });
  es.addEventListener('ping', () => {
    state.sseState = 'live';
    updateLinkStatus();
  });
  es.addEventListener('error', () => {
    state.sseState = state.summary ? 'reconnecting' : 'offline';
    updateLinkStatus();
  });
}

function applySummary(summary, fromSSE) {
  if (!summary || !Array.isArray(summary.devices)) return;
  state.summary = summary;
  if (fromSSE) pushHistory(summary.devices);
  updateHeader();
  updateSidebar();
  updateLinkStatus();
  onSummaryChanged();
}

function onSummaryChanged() {
  const route = state.route;
  if (route.name === 'overview') {
    if (state.renderedRoute === 'overview') updateOverview();
    else renderOverview();
  } else if (route.name === 'ports') {
    if (state.renderedRoute === 'ports') updatePorts();
    else renderPorts();
  } else if (route.name === 'device') {
    if (state.device) updateDeviceDynamic();
  } else if (route.name === 'settings') {
    updateSettingsDynamic();
  }
}

function pushHistory(devices) {
  for (const dev of devices) {
    let h = state.history.get(dev.id);
    if (!h) {
      h = { top: [], mid: [], bottom: [] };
      state.history.set(dev.id, h);
    }
    for (const zone of ZONES) {
      const value = dev.th && dev.th[zone] ? Number(dev.th[zone].temp) : NaN;
      h[zone].push(Number.isFinite(value) ? value : NaN);
      if (h[zone].length > 120) h[zone].shift();
    }
  }
}

/* ----------------------------------------------------------------- spark */

function drawLineCanvas(canvas, series) {
  if (!canvas) return;
  const dpr = window.devicePixelRatio || 1;
  const w = Math.max(40, canvas.clientWidth || 220);
  const h = Math.max(20, canvas.clientHeight || 44);
  canvas.width = Math.round(w * dpr);
  canvas.height = Math.round(h * dpr);
  const ctx = canvas.getContext('2d');
  if (!ctx) return;
  ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
  ctx.clearRect(0, 0, w, h);

  const all = [];
  for (const s of series) {
    for (const v of s.values) if (Number.isFinite(v)) all.push(v);
  }
  ctx.strokeStyle = 'rgba(34,49,66,.7)';
  ctx.lineWidth = 1;
  ctx.beginPath();
  ctx.moveTo(0, h - 0.5);
  ctx.lineTo(w, h - 0.5);
  ctx.stroke();
  if (all.length === 0) return;

  let min = Math.min.apply(null, all);
  let max = Math.max.apply(null, all);
  if (max - min < 0.4) {
    const mid = (min + max) / 2;
    min = mid - 0.2;
    max = mid + 0.2;
  }
  const pad = (max - min) * 0.14;
  min -= pad;
  max += pad;

  ctx.setLineDash([3, 4]);
  ctx.strokeStyle = 'rgba(147,163,181,.18)';
  ctx.beginPath();
  ctx.moveTo(0, h / 2);
  ctx.lineTo(w, h / 2);
  ctx.stroke();
  ctx.setLineDash([]);

  for (const s of series) {
    if (!s.values || s.values.length < 2) continue;
    ctx.strokeStyle = s.color;
    ctx.lineWidth = s.width || 1.4;
    ctx.lineJoin = 'round';
    ctx.lineCap = 'round';
    ctx.beginPath();
    let started = false;
    const n = s.values.length;
    for (let i = 0; i < n; i++) {
      const v = s.values[i];
      if (!Number.isFinite(v)) {
        started = false;
        continue;
      }
      const x = (i / (n - 1)) * (w - 4) + 2;
      const y = h - 2 - ((v - min) / (max - min)) * (h - 4);
      if (!started) {
        ctx.moveTo(x, y);
        started = true;
      } else {
        ctx.lineTo(x, y);
      }
    }
    ctx.stroke();
  }
}

function drawDeviceSpark(canvas, id) {
  const h = state.history.get(id);
  if (!h) return;
  drawLineCanvas(canvas, [
    { values: h.top, color: ZONE_COLOR.top },
    { values: h.mid, color: 'rgba(147,163,181,.65)' },
    { values: h.bottom, color: ZONE_COLOR.bottom },
  ]);
}

function drawZoneSpark(canvas, id, zone) {
  const h = state.history.get(id);
  if (!h) return;
  drawLineCanvas(canvas, [{ values: h[zone], color: ZONE_COLOR[zone], width: 1.6 }]);
}

/* ----------------------------------------------------------------- shell */

function navLink(name, label, num) {
  return '<a href="#/' + name + '" data-nav="' + name + '"><span class="nav-icon">' + num + '</span><span>' + label + '</span></a>';
}

function renderShell() {
  document.getElementById('app').innerHTML =
    '<div class="shell">' +
      '<aside class="sidebar">' +
        '<div class="brand">' +
          '<div class="brand-mark">U42</div>' +
          '<div><div class="brand-name">iADU-SIM</div><div class="brand-sub">Asset Strip Emulator</div></div>' +
        '</div>' +
        '<nav class="nav" aria-label="主导航">' +
          navLink('overview', '总览', '01') +
          navLink('ports', '端口', '02') +
          navLink('logs', '实时日志', '03') +
          navLink('settings', '设置', '04') +
        '</nav>' +
        '<div class="sidebar-devices" id="sidebar-devices"></div>' +
        '<div class="sidebar-foot">' +
          '<span id="foot-version">iadu-sim</span>' +
          '<span id="foot-state">正在连接…</span>' +
        '</div>' +
      '</aside>' +
      '<div class="main">' +
        '<header class="topbar">' +
          '<div><div class="crumb" id="crumb">IADU-SIM / CONSOLE</div><h1 id="page-title">总览</h1></div>' +
          '<div class="spacer"></div>' +
          '<div class="link-status offline" id="link-status"><span class="dot"></span><span id="link-text">未连接</span></div>' +
          '<button class="btn small ghost" id="token-btn" type="button">访问令牌</button>' +
        '</header>' +
        '<main class="content" id="main"><div class="loading">正在加载控制台…</div></main>' +
      '</div>' +
    '</div>';
  const tokenBtn = document.getElementById('token-btn');
  if (tokenBtn) tokenBtn.addEventListener('click', openTokenModal);
}

function updateHeader() {
  const s = state.summary;
  const footVersion = document.getElementById('foot-version');
  if (footVersion) footVersion.textContent = s ? 'iadu-sim v' + s.version : 'iadu-sim';
  const footState = document.getElementById('foot-state');
  if (footState) {
    footState.textContent = s
      ? s.devices.length + ' 设备 · ' + s.ports.length + ' 端口'
      : '等待数据…';
  }
  const tokenBtn = document.getElementById('token-btn');
  if (tokenBtn) tokenBtn.textContent = state.token ? '令牌 ●' : '访问令牌';
  setPageTitle();
}

function setPageTitle() {
  const title = document.getElementById('page-title');
  const crumb = document.getElementById('crumb');
  if (!title || !crumb) return;
  const route = state.route;
  if (route.name === 'device') {
    const dev = state.summary ? state.summary.devices.find((d) => d.id === route.id) : null;
    title.textContent = dev ? dev.name : '设备详情';
    crumb.textContent = 'IADU-SIM / DEVICE / ' + route.id;
  } else {
    const map = {
      overview: ['总览', 'OVERVIEW'],
      ports: ['端口', 'PORTS'],
      logs: ['实时帧日志', 'FRAME LOG'],
      settings: ['设置', 'SETTINGS'],
    };
    const item = map[route.name] || map.overview;
    title.textContent = item[0];
    crumb.textContent = 'IADU-SIM / ' + item[1];
  }
}

function setActiveNav() {
  const active = state.route.name === 'device' ? 'overview' : state.route.name;
  qsa('[data-nav]').forEach((a) => {
    a.classList.toggle('active', a.dataset.nav === active);
  });
}

function updateSidebar() {
  const box = document.getElementById('sidebar-devices');
  if (!box) return;
  const devices = state.summary ? state.summary.devices : [];
  const sig = devices.map((d) => d.id + '|' + d.name).join(';');
  if (sig !== state.sidebarSig) {
    state.sidebarSig = sig;
    if (devices.length === 0) {
      box.innerHTML = '<div class="sidebar-heading">设备</div><div class="subtle sidebar-empty">暂无设备</div>';
    } else {
      box.innerHTML = '<div class="sidebar-heading">设备</div>' + devices.map((d) =>
        '<a class="device-link' + (state.route.name === 'device' && state.route.id === d.id ? ' active' : '') +
        '" href="#/devices/' + encodeURIComponent(d.id) + '" data-sidebar-device="' + esc(d.id) + '">' +
        '<span class="dot idle"></span><span>' + esc(d.name || d.id) + '</span></a>').join('');
    }
  }
  for (const dev of devices) {
    const link = Array.from(box.querySelectorAll('[data-sidebar-device]'))
      .find((el) => el.dataset.sidebarDevice === dev.id);
    if (!link) continue;
    link.classList.toggle('active', state.route.name === 'device' && state.route.id === dev.id);
    const dot = link.querySelector('.dot');
    if (dot) dot.className = 'dot ' + (deviceOnline(dev) ? 'online' : 'idle');
  }
}

function deviceOnline(dev) {
  if (dev.last_seen) {
    const seen = parseTime(dev.last_seen);
    if (seen && Date.now() - seen.getTime() < 5000) return true;
  }
  if (!state.summary) return false;
  const ports = new Map(state.summary.ports.map((p) => [p.name, p]));
  return (dev.bindings || []).some((b) => {
    const p = ports.get(b.port);
    return p && p.connected;
  });
}

/* ---------------------------------------------------------------- routing */

function route() {
  const raw = location.hash.replace(/^#\/?/, '');
  const parts = raw.split('/').filter(Boolean);
  let r;
  if (parts[0] === 'devices' && parts[1]) {
    r = { name: 'device', id: decodeURIComponent(parts[1]) };
  } else if (['overview', 'ports', 'logs', 'settings'].indexOf(parts[0]) >= 0) {
    r = { name: parts[0] };
  } else {
    r = { name: 'overview' };
  }
  state.route = r;
  state.renderedRoute = '';
  setActiveNav();
  setPageTitle();

  if (r.name === 'overview') {
    if (state.summary) renderOverview(); else setMain('<div class="loading">正在同步设备状态…</div>');
  } else if (r.name === 'ports') {
    if (state.summary) renderPorts(); else setMain('<div class="loading">正在同步端口状态…</div>');
  } else if (r.name === 'logs') {
    renderLogs();
  } else if (r.name === 'settings') {
    renderSettings();
  } else if (r.name === 'device') {
    renderDeviceRoute(r.id);
  }
}

function setMain(html) {
  const main = document.getElementById('main');
  if (main) main.innerHTML = html;
}

/* --------------------------------------------------------------- overview */

function deviceSignature(devices) {
  return devices.map((d) => [
    d.id, d.name, d.tcp_unit_id, d.u_count, d.climate_enabled,
    (d.bindings || []).map((b) => b.port + '/' + b.unit_id).join(','),
  ].join('|')).join(';');
}

function onlinePortCount() {
  if (!state.summary) return 0;
  return state.summary.ports.filter((p) => p.connected).length;
}

function totalFrames() {
  if (!state.summary) return 0;
  let n = 0;
  for (const p of state.summary.ports) n += (p.rx || 0) + (p.tx || 0);
  if (state.summary.mbap) n += (state.summary.mbap.rx || 0) + (state.summary.mbap.tx || 0);
  return n;
}

function tileHTML(id, label, value, sub) {
  return '<div class="tile"><div class="k">' + label + '</div>' +
    '<div class="v" id="' + id + '">' + value + '</div>' +
    '<div class="s" id="' + id + '-sub">' + sub + '</div></div>';
}

function renderOverview() {
  if (!state.summary) return;
  const s = state.summary;
  const devices = s.devices || [];
  const ports = s.ports || [];
  const main = document.getElementById('main');
  if (!main) return;
  main.innerHTML = '<div class="content-inner">' +
    '<div class="tiles">' +
      tileHTML('tile-devices', '设备', String(devices.length), '一台寄存器 = 两个服务') +
      tileHTML('tile-ports', 'RTU 端口在线', onlinePortCount() + ' / ' + ports.length, '纯 Go pty 虚拟串口') +
      tileHTML('tile-frames', '帧总量', String(totalFrames()), 'RTU + Modbus TCP') +
      tileHTML('tile-tcp', 'Modbus TCP', s.tcp_listen ? '在线' : '已关闭',
        s.tcp_listen ? esc(displayListen(s.tcp_listen)) : '调整服务未启动') +
    '</div>' +
    '<div class="card-head">' +
      '<div><div class="card-title">模拟设备</div><div class="card-sub">RTU 从站 · 影子寄存器调整</div></div>' +
      '<div class="spacer"></div>' +
      '<button class="btn primary" id="add-device" type="button">添加设备</button>' +
    '</div>' +
    '<div class="device-grid" id="device-grid"></div>' +
  '</div>';

  const addBtn = document.getElementById('add-device');
  if (addBtn) addBtn.addEventListener('click', openAddDeviceModal);
  const grid = document.getElementById('device-grid');
  if (devices.length === 0) {
    grid.innerHTML = '<div class="empty"><strong>还没有模拟设备</strong>' +
      '按右上角「添加设备」创建一台 42U 资产条。</div>';
  } else {
    grid.innerHTML = devices.map(deviceCardHTML).join('');
    requestAnimationFrame(() => {
      qsa('canvas[data-spark]').forEach((c) => drawDeviceSpark(c, c.dataset.spark));
    });
  }
  state.overviewSig = deviceSignature(devices);
  state.renderedRoute = 'overview';
  updateOverviewTiles();
}

function deviceCardHTML(dev) {
  const online = deviceOnline(dev);
  const mode = dev.manual_mask ? '手动' : (dev.climate_enabled ? '自动' : '固定');
  const modeClass = dev.manual_mask ? 'manual' : 'auto';
  const bindings = (dev.bindings || []).map((b) =>
    '<span class="binding-chip">' + esc(b.port) + ':' + b.unit_id + '</span>').join('');
  return '<article class="device-card' + (online ? '' : ' idle') + '" data-device-card="' + esc(dev.id) + '">' +
    '<div class="device-head">' +
      '<span class="dot ' + (online ? 'online' : 'idle') + '" data-online-dot></span>' +
      '<div class="device-title"><h3>' + esc(dev.name || dev.id) + '</h3>' +
        '<div class="id">' + esc(dev.id) + ' · TCP #' + dev.tcp_unit_id + '</div></div>' +
      '<div class="spacer"></div>' +
      '<span class="badge tcp">TCP #' + dev.tcp_unit_id + '</span>' +
    '</div>' +
    '<div class="bindings">' + (bindings || '<span class="binding-chip">未绑定串口</span>') + '</div>' +
    '<div class="th-grid">' +
      zoneColumnHTML(dev, 'top') + zoneColumnHTML(dev, 'mid') + zoneColumnHTML(dev, 'bottom') +
    '</div>' +
    '<canvas class="spark" data-spark="' + esc(dev.id) + '" aria-label="温度趋势"></canvas>' +
    '<div class="device-foot">' +
      '<span data-present>' + dev.present_count + ' / ' + dev.u_count + ' 在位</span>' +
      '<span data-seen title="' + esc(dev.last_seen || '') + '">' + relTime(dev.last_seen) + '</span>' +
      '<span class="badge ' + modeClass + '" data-mode>' + mode + '</span>' +
      '<a class="link" href="#/devices/' + encodeURIComponent(dev.id) + '">详情 →</a>' +
    '</div>' +
  '</article>';
}

function zoneColumnHTML(dev, zone) {
  const th = dev.th && dev.th[zone] ? dev.th[zone] : { temp: NaN, humi: NaN };
  return '<div class="zone-col" data-zone-col="' + zone + '">' +
    '<div class="label">' + ZONE_TAG[zone] + ' · ' + ZONE_LABEL[zone] + '</div>' +
    '<div class="temp"><span data-th-temp="' + zone + '">' + fmtNum(th.temp) + '</span><span class="u">℃</span></div>' +
    '<div class="humi"><span data-th-humi="' + zone + '">' + fmtNum(th.humi) + '</span> %RH</div>' +
  '</div>';
}

function updateOverviewTiles() {
  if (!state.summary) return;
  const s = state.summary;
  const devices = s.devices || [];
  const ports = s.ports || [];
  const set = (id, value, sub) => {
    const el = document.getElementById(id);
    if (el) el.textContent = value;
    const subEl = document.getElementById(id + '-sub');
    if (subEl && sub !== undefined) subEl.textContent = sub;
  };
  set('tile-devices', String(devices.length));
  set('tile-ports', onlinePortCount() + ' / ' + ports.length);
  set('tile-frames', String(totalFrames()));
  set('tile-tcp', s.tcp_listen ? '在线' : '已关闭', s.tcp_listen ? displayListen(s.tcp_listen) : '调整服务未启动');
}

function updateOverview() {
  if (!state.summary || !document.getElementById('device-grid')) return;
  const s = state.summary;
  const devices = s.devices || [];
  const sig = deviceSignature(devices);
  if (sig !== state.overviewSig) {
    renderOverview();
    return;
  }
  updateOverviewTiles();
  for (const dev of devices) {
    const card = qsa('[data-device-card]').find((el) => el.dataset.deviceCard === dev.id);
    if (!card) continue;
    const online = deviceOnline(dev);
    card.classList.toggle('idle', !online);
    const dot = card.querySelector('[data-online-dot]');
    if (dot) dot.className = 'dot ' + (online ? 'online' : 'idle');
    for (const zone of ZONES) {
      const temp = card.querySelector('[data-th-temp="' + zone + '"]');
      const humi = card.querySelector('[data-th-humi="' + zone + '"]');
      if (temp && dev.th && dev.th[zone]) temp.textContent = fmtNum(dev.th[zone].temp);
      if (humi && dev.th && dev.th[zone]) humi.textContent = fmtNum(dev.th[zone].humi);
    }
    const present = card.querySelector('[data-present]');
    if (present) present.textContent = dev.present_count + ' / ' + dev.u_count + ' 在位';
    const seen = card.querySelector('[data-seen]');
    if (seen) {
      seen.textContent = relTime(dev.last_seen);
      seen.title = dev.last_seen || '';
    }
    const mode = card.querySelector('[data-mode]');
    if (mode) {
      mode.textContent = dev.manual_mask ? '手动' : (dev.climate_enabled ? '自动' : '固定');
      mode.className = 'badge ' + (dev.manual_mask ? 'manual' : 'auto');
    }
    const spark = card.querySelector('canvas[data-spark]');
    if (spark) drawDeviceSpark(spark, dev.id);
  }
}

/* ----------------------------------------------------------------- device */

async function renderDeviceRoute(id) {
  state.deviceId = id;
  state.device = null;
  const token = ++state.deviceViewToken;
  state.renderedRoute = '';
  setMain('<div class="loading">正在加载设备 ' + esc(id) + '…</div>');
  try {
    const detail = await api('/api/devices/' + encodeURIComponent(id));
    if (token !== state.deviceViewToken) return;
    state.device = detail;
    renderDevice(detail);
  } catch (err) {
    if (token !== state.deviceViewToken) return;
    setMain('<div class="error-box">无法加载设备：' + esc(err.message) + '</div>');
    reportError(err);
  }
}

function zoneCardHTML(detail, zone) {
  const th = detail.th && detail.th[zone] ? detail.th[zone] : { temp: NaN, humi: NaN, temp_raw: 0, humi_raw: 0 };
  const manual = Boolean(detail.status && (detail.status.manual_mask & (1 << ZONES.indexOf(zone))));
  const raw = ((th.temp_raw << 8) | th.humi_raw) & 0xffff;
  return '<div class="zone-card' + (manual ? ' manual' : '') + '" data-zone-card="' + zone + '">' +
    '<div class="zone-name">' + ZONE_TAG[zone] + ' · ' + ZONE_LABEL[zone] + '</div>' +
    '<div class="zone-temp"><span data-zone-temp="' + zone + '">' + fmtNum(th.temp) + '</span><span class="u"> ℃</span></div>' +
    '<div class="zone-meta"><span data-zone-humi="' + zone + '">' + fmtNum(th.humi) + ' %RH</span>' +
      '<span class="dim" data-zone-raw="' + zone + '">raw 0x' + hex4(raw) + '</span>' +
      '<span class="badge ' + (manual ? 'manual' : 'auto') + '" data-zone-mode="' + zone + '">' + (manual ? '手动' : '自动') + '</span>' +
    '</div>' +
    '<canvas class="zone-spark" data-zone-spark="' + zone + '" aria-label="' + ZONE_LABEL[zone] + '温度趋势"></canvas>' +
    '<div class="zone-actions">' +
      '<button class="btn small ghost" type="button" data-zone-action="manual" data-zone="' + zone + '">手动设置</button>' +
      '<button class="btn small ghost" type="button" data-zone-action="auto" data-zone="' + zone + '"' + (manual ? '' : ' disabled') + '>恢复自动</button>' +
    '</div>' +
  '</div>';
}

function uGridHTML(detail) {
  const snap = detail.snapshot;
  const status = detail.status || {};
  const cells = [];
  for (let u = 1; u <= snap.u_count; u++) {
    const present = bitSet(status.present, u - 1);
    const changed = bitSet(status.changed, u - 1);
    const cls = 'u-cell' + (present ? ' present' : '') + (changed ? ' changed' : '');
    const tag = snap.tags && snap.tags[u - 1] ? snap.tags[u - 1] : '';
    cells.push('<button class="' + cls + '" type="button" data-u-cell="' + u + '" title="' + esc('U' + u + (tag ? ' · ' + tag : ' · 空')) + '">' +
      '<span>U' + String(u).padStart(2, '0') + '</span><span class="u-dot"></span></button>');
  }
  return '<div class="u-grid">' + cells.join('') + '</div>';
}

function ledGridHTML(detail) {
  const snap = detail.snapshot;
  const cells = [];
  for (let u = 1; u <= snap.u_count; u++) {
    const value = snap.led && snap.led[u - 1] ? snap.led[u - 1] : 0;
    cells.push('<button class="led-cell' + (value ? ' lit' : '') + '" type="button" data-led-cell="' + u + '">0x' + hex4(value) + '</button>');
  }
  return '<div class="led-grid">' + cells.join('') + '</div>';
}

function bindingsHTML(detail) {
  const snap = detail.snapshot;
  const ports = state.summary ? state.summary.ports : [];
  const rows = (detail.bindings || []).map((b) =>
    '<div class="binding-row"><span class="grow">' + esc(b.port) + ' · unit ' + b.unit_id + '</span>' +
      '<button class="btn small danger" type="button" data-remove-binding data-port="' + esc(b.port) + '" data-unit="' + b.unit_id + '">移除</button></div>').join('');
  const options = ports.map((p) => '<option value="' + esc(p.name) + '">' + esc(p.name) + '</option>').join('');
  return '<div class="binding-list">' + (rows || '<div class="subtle">未绑定任何串口</div>') + '</div>' +
    '<div class="info-grid mt-10">' +
      '<div class="field"><label>追加绑定端口</label><select id="binding-port">' + options + '</select></div>' +
      '<div class="field"><label>从站地址（1-247）</label><input id="binding-unit" type="number" min="1" max="247" value="1"></div>' +
    '</div>' +
    '<div class="form-actions"><button class="btn" type="button" id="add-binding"' + (ports.length ? '' : ' disabled') + '>添加绑定</button></div>' +
    '<div class="section-note">删除最后一个绑定后，设备仍可通过 Modbus TCP / 页面访问。</div>';
}

function climateFormHTML(detail) {
  const c = detail.snapshot.climate || {};
  const t = c.temperature || {};
  const h = c.humidity || {};
  const z = c.zone_offsets || {};
  const n = c.noise || {};
  const fixed = detail.snapshot.fixed_th || [25, 50];
  const field = (id, label, value, step) =>
    '<div class="field"><label for="' + id + '">' + label + '</label><input id="' + id + '" type="number" step="' + (step || '0.1') + '" value="' + Number(value || 0) + '"></div>';
  const zoneFields = (prefix, label, off) => field('c-zt-' + prefix, label + ' 温度偏移', off.temp) +
    field('c-zh-' + prefix, label + ' 湿度偏移', off.humi) + field('c-zl-' + prefix, label + ' 滞后(h)', off.lag_h);
  return '<details class="ref"><summary>展开动态温湿度参数</summary>' +
    '<div class="info-grid mt-12">' +
      '<label class="checkline"><input id="c-enabled" type="checkbox"' + (c.enabled ? ' checked' : '') + '> 启用动态温湿度</label>' +
      '<div class="field"><label for="c-timezone">时区</label><input id="c-timezone" value="' + esc(c.timezone || 'Asia/Shanghai') + '"></div>' +
      field('c-timescale', '时间加速倍数', c.time_scale || 1, '0.1') +
      field('c-update', '更新周期 (ms)', c.update_ms || 5000, '100') +
      field('c-tmean', '日均温度 (℃)', t.mean || 24.5) +
      field('c-tamp', '日振幅 (℃)', t.amplitude || 1.6) +
      field('c-tpeak', '峰值时刻 (h)', t.peak_hour || 15, '0.1') +
      field('c-th2', '二次谐波振幅', t.harmonic2_amplitude || 0.2, '0.1') +
      field('c-tmin', '温度下限 (℃)', t.min || 18) +
      field('c-tmax', '温度上限 (℃)', t.max || 30) +
      field('c-hmean', '日均湿度 (%)', h.mean || 50) +
      field('c-hcouple', '温度耦合系数', h.temp_coupling || 1.2, '0.1') +
      field('c-hamp', '湿度日振幅', h.amplitude || 1.5, '0.1') +
      field('c-hmin', '湿度下限 (%)', h.min || 30) +
      field('c-hmax', '湿度上限 (%)', h.max || 70) +
      zoneFields('top', '上部', z.top || {}) +
      zoneFields('mid', '中部', z.mid || {}) +
      zoneFields('bottom', '下部', z.bottom || {}) +
      field('c-ntau', '温度噪声 τ (ms)', n.temp_tau_ms || 180000, '1000') +
      field('c-nsigma', '温度噪声 σ (℃)', n.temp_sigma || 0.15, '0.01') +
      field('c-htau', '湿度噪声 τ (ms)', n.humi_tau_ms || 180000, '1000') +
      field('c-hsigma', '湿度噪声 σ (%)', n.humi_sigma || 1, '0.1') +
      field('c-fixed-temp', '固定温度 (℃, 关闭动态时)', fixed[0], '0.1') +
      field('c-fixed-humi', '固定湿度 (%, 关闭动态时)', fixed[1], '0.1') +
    '</div>' +
    '<div class="form-actions"><button class="btn primary" type="button" id="save-climate">保存温湿度参数</button></div>' +
  '</details>';
}

function renderDevice(detail) {
  const snap = detail.snapshot;
  const main = document.getElementById('main');
  if (!main) return;
  const scroll = main.scrollTop;
  const bindings = (detail.bindings || []).map((b) =>
    '<span class="binding-chip">' + esc(b.port) + ':' + b.unit_id + '</span>').join('');
  const mode = (detail.status && detail.status.manual_mask) ? '手动' : (snap.climate.enabled ? '自动' : '固定');
  const modeClass = (detail.status && detail.status.manual_mask) ? 'manual' : 'auto';
  const bus = snap.bus || [1, 1, 0];
  const busLabels = ['资产条连接', '总线通讯正常', '电流过大'];
  main.innerHTML = '<div class="content-inner">' +
    '<div class="device-hero">' +
      '<div><a class="subtle" href="#/overview">← 返回总览</a>' +
        '<h2>' + esc(snap.name || snap.id) + '</h2>' +
        '<div class="meta"><span class="badge tcp">TCP #' + snap.tcp_unit_id + '</span>' +
          '<span class="badge">' + snap.u_count + 'U</span>' +
          '<span class="badge ' + modeClass + '" data-device-mode>' + mode + '</span>' +
          '<span class="badge">' + esc(snap.id) + '</span>' +
          bindings + '</div></div>' +
      '<div class="spacer"></div>' +
      '<div class="card-actions">' +
        '<button class="btn small ghost" type="button" data-action="pulse-all">触发全部变化</button>' +
        '<button class="btn small ghost" type="button" data-action="clear-changed">清除变化位</button>' +
        '<button class="btn small danger" type="button" data-action="delete">删除设备</button>' +
      '</div>' +
    '</div>' +

    '<div class="zone-grid">' + ZONES.map((z) => zoneCardHTML(detail, z)).join('') + '</div>' +

    '<div class="card mt-16">' +
      '<div class="card-head"><div><div class="card-title">U 位与标签</div>' +
        '<div class="card-sub">在位 <span data-count-present>' + (detail.status ? countBits(detail.status.present) : 0) + '</span> / ' + snap.u_count +
        ' · 变化中 <span data-count-changed>' + (detail.status ? countBits(detail.status.changed) : 0) + '</span></div></div>' +
        '<div class="spacer"></div><span class="subtle">点击任意 U 位编辑标签</span></div>' +
      uGridHTML(detail) +
    '</div>' +

    '<div class="card">' +
      '<div class="card-head"><div class="card-title">总线状态（FC04 0x0103）</div></div>' +
      '<div class="card-actions">' +
        bus.map((v, i) => '<button class="chip" type="button" data-bus-index="' + i + '" aria-pressed="' + (v ? 'true' : 'false') + '"><span class="dot"></span>' + busLabels[i] + '</button>').join('') +
      '</div>' +
      '<div class="section-note">点击切换并立即写入模拟寄存器；采集端下一轮即可读到。</div>' +
    '</div>' +

    '<div class="card">' +
      '<div class="card-head"><div><div class="card-title">设备信息</div><div class="card-sub">FC03 0x0000-0x0005 · 0x0001 地址随串口绑定变化</div></div></div>' +
      '<div class="info-grid">' +
        '<div class="field"><label for="f-name">设备名称</label><input id="f-name" value="' + esc(snap.name) + '"></div>' +
        '<div class="field"><label for="f-esn">ESN（≤20 ASCII）</label><input id="f-esn" maxlength="20" value="' + esc(snap.esn) + '"></div>' +
        '<div class="field"><label for="f-model">型号（hex）</label><input id="f-model" value="' + hex4(snap.model) + '"></div>' +
        '<div class="field"><label for="f-baud">波特率码</label><input id="f-baud" type="number" min="0" max="65535" value="' + snap.baud_code + '"></div>' +
        '<div class="field"><label for="f-soft">软件版本（hex）</label><input id="f-soft" value="' + hex4(snap.soft_version) + '"></div>' +
        '<div class="field"><label for="f-hard">固件版本（hex）</label><input id="f-hard" value="' + hex4(snap.hard_version) + '"></div>' +
        '<div class="field"><label for="f-ucount">U 位长度</label><input id="f-ucount" type="number" min="1" max="42" value="' + snap.u_count + '"></div>' +
        '<div class="field"><label for="f-tcpunit">Modbus TCP unit</label><input id="f-tcpunit" type="number" min="1" max="247" value="' + snap.tcp_unit_id + '"></div>' +
        '<div class="field"><label for="f-hold">变化位保持 (ms)</label><input id="f-hold" type="number" min="1" max="3600000" value="' + snap.changed_hold_ms + '"></div>' +
        '<label class="checkline"><input id="f-adopt" type="checkbox"' + (snap.adopt_address ? ' checked' : '') + '> 允许 FC06 地址协商</label>' +
        '<label class="checkline"><input id="f-strict" type="checkbox"' + (snap.strict ? ' checked' : '') + '> 严格地址模式</label>' +
      '</div>' +
      '<div class="form-actions"><button class="btn primary" type="button" id="save-info">保存设备信息</button></div>' +
    '</div>' +

    '<div class="card">' +
      '<div class="card-head"><div><div class="card-title">串口绑定</div><div class="card-sub">同一设备可绑定多个端口共享寄存器</div></div></div>' +
      bindingsHTML(detail) +
    '</div>' +

    '<div class="card">' +
      '<div class="card-head"><div><div class="card-title">LED 寄存器</div><div class="card-sub">FC03 / FC06 0x0100 + U - 1</div></div>' +
        '<div class="spacer"></div><span class="subtle">点击设置 16 位值</span></div>' +
      ledGridHTML(detail) +
    '</div>' +

    '<div class="card">' +
      '<div class="card-head"><div><div class="card-title">温湿度模型</div><div class="card-sub">日夜曲线 + OU 噪声 + 手动覆盖</div></div></div>' +
      climateFormHTML(detail) +
    '</div>' +

    shadowMapHTML() +
  '</div>';

  bindDeviceEvents(detail);
  requestAnimationFrame(() => {
    qsa('[data-zone-spark]').forEach((c) => drawZoneSpark(c, snap.id, c.dataset.zoneSpark));
  });
  state.renderedRoute = 'device:' + snap.id;
  main.scrollTop = scroll;
  setPageTitle();
  updateSidebar();
}

function shadowMapHTML() {
  return '<details class="ref"><summary>Modbus TCP 影子寄存器映射（只读参考）</summary>' +
    '<table><thead><tr><th>地址</th><th>内容</th><th>读写</th></tr></thead><tbody>' +
      '<tr><td>0x8000-0x8005</td><td>型号 / 地址 / 波特率码 / U 位长度 / 软件 / 固件</td><td>只读</td></tr>' +
      '<tr><td>0x8006-0x800F</td><td>ESN（20 ASCII）</td><td>读写</td></tr>' +
      '<tr><td>0x8010-0x80B7</td><td>U 位标签，U(i)=0x8010+(i-1)*4</td><td>读写</td></tr>' +
      '<tr><td>0x80C0-0x80C2</td><td>上/中/下温湿度 raw；写入即冻结该区</td><td>读写</td></tr>' +
      '<tr><td>0x80C3-0x80C5</td><td>连接 / 通讯正常 / 过流</td><td>读写</td></tr>' +
      '<tr><td>0x80D0-0x80F9</td><td>U1-U42 LED 值</td><td>读写</td></tr>' +
      '<tr><td>0x8100-0x8102</td><td>在位位图（42 位，LSB 先行）</td><td>读写</td></tr>' +
      '<tr><td>0x8110-0x8112</td><td>变化位图</td><td>读写</td></tr>' +
      '<tr><td>0x8120</td><td>命令：1=清变化位，2=全部恢复自动</td><td>写</td></tr>' +
      '<tr><td>0x8121</td><td>手动覆盖掩码（bit0 上 / bit1 中 / bit2 下）</td><td>只读</td></tr>' +
      '<tr><td>线圈 0x8000-0x8029</td><td>在位；0x8030-0x8059 变化；0x8060-0x8062 手动；0x8070 清变化</td><td>读写</td></tr>' +
    '</tbody></table>' +
    '<div class="section-note">FC04 在上表同地址提供只读镜像；TCP 服务只暴露 0x8000 起始的影子区，与 RTU 原生地址不冲突。</div>' +
  '</details>';
}

function bindDeviceEvents(detail) {
  const main = document.getElementById('main');
  if (!main) return;
  qsa('[data-u-cell]', main).forEach((btn) => {
    btn.addEventListener('click', () => openTagModal(Number(btn.dataset.uCell)));
  });
  qsa('[data-led-cell]', main).forEach((btn) => {
    btn.addEventListener('click', () => openLedModal(Number(btn.dataset.ledCell)));
  });
  qsa('[data-zone-action]', main).forEach((btn) => {
    const zone = btn.dataset.zone;
    if (btn.dataset.zoneAction === 'manual') {
      btn.addEventListener('click', () => openManualModal(zone));
    } else {
      btn.addEventListener('click', async () => {
        try {
          const result = await api('/api/devices/' + encodeURIComponent(detail.snapshot.id) +
            '/climate/override?zone=' + encodeURIComponent(zone), { method: 'DELETE' });
          applyDevice(result, '已恢复自动');
        } catch (err) {
          reportError(err);
        }
      });
    }
  });
  qsa('[data-bus-index]', main).forEach((btn) => {
    btn.addEventListener('click', async () => {
      try {
        const snap = state.device ? state.device.snapshot : detail.snapshot;
        const bus = (snap.bus || [1, 1, 0]).map((v) => Boolean(v));
        const idx = Number(btn.dataset.busIndex);
        bus[idx] = !bus[idx];
        const result = await api('/api/devices/' + encodeURIComponent(snap.id) + '/bus', {
          method: 'PUT',
          body: { connected: bus[0], comm_ok: bus[1], overcurrent: bus[2] },
        });
        applyDevice(result, '总线状态已更新');
      } catch (err) {
        reportError(err);
      }
    });
  });
  const pulseAll = main.querySelector('[data-action="pulse-all"]');
  if (pulseAll) pulseAll.addEventListener('click', () => pulseChanged(0));
  const clearChanged = main.querySelector('[data-action="clear-changed"]');
  if (clearChanged) clearChanged.addEventListener('click', async () => {
    try {
      const result = await api('/api/devices/' + encodeURIComponent(detail.snapshot.id) + '/changed/clear', { method: 'POST' });
      applyDevice(result, '变化位已清除');
    } catch (err) {
      reportError(err);
    }
  });
  const deleteBtn = main.querySelector('[data-action="delete"]');
  if (deleteBtn) deleteBtn.addEventListener('click', deleteDevice);
  const saveInfo = document.getElementById('save-info');
  if (saveInfo) saveInfo.addEventListener('click', saveInfoForm);
  const addBinding = document.getElementById('add-binding');
  if (addBinding) addBinding.addEventListener('click', addBindingForm);
  qsa('[data-remove-binding]', main).forEach((btn) => {
    btn.addEventListener('click', () => removeBinding(btn.dataset.port, Number(btn.dataset.unit)));
  });
  const saveClimate = document.getElementById('save-climate');
  if (saveClimate) saveClimate.addEventListener('click', saveClimateForm);
}

function applyDevice(detail, message) {
  if (!detail || !detail.snapshot) return;
  state.device = detail;
  if (state.route.name === 'device' && state.route.id === detail.snapshot.id) {
    renderDevice(detail);
  }
  if (message) toast(message);
}

function updateDeviceDynamic() {
  if (!state.device || !state.summary) return;
  const summaryDev = state.summary.devices.find((d) => d.id === state.device.snapshot.id);
  if (!summaryDev) {
    toast('设备已被移除', 'warn');
    navigate('#/overview');
    return;
  }
  for (const zone of ZONES) {
    const temp = document.querySelector('[data-zone-temp="' + zone + '"]');
    const humi = document.querySelector('[data-zone-humi="' + zone + '"]');
    const mode = document.querySelector('[data-zone-mode="' + zone + '"]');
    const card = document.querySelector('[data-zone-card="' + zone + '"]');
    const rawEl = document.querySelector('[data-zone-raw="' + zone + '"]');
    const manual = Boolean(summaryDev.manual_mask & (1 << ZONES.indexOf(zone)));
    if (temp) temp.textContent = fmtNum(summaryDev.th[zone].temp);
    if (humi) humi.textContent = fmtNum(summaryDev.th[zone].humi) + ' %RH';
    if (rawEl && summaryDev.th[zone]) {
      const word = ((summaryDev.th[zone].temp_raw << 8) | summaryDev.th[zone].humi_raw) & 0xffff;
      rawEl.textContent = 'raw 0x' + hex4(word);
    }
    if (mode) {
      mode.textContent = manual ? '手动' : '自动';
      mode.className = 'badge ' + (manual ? 'manual' : 'auto');
    }
    if (card) card.classList.toggle('manual', manual);
    const spark = document.querySelector('[data-zone-spark="' + zone + '"]');
    if (spark) drawZoneSpark(spark, summaryDev.id, zone);
  }
  const present = document.querySelector('[data-count-present]');
  if (present) present.textContent = String(summaryDev.present_count);
  const changed = document.querySelector('[data-count-changed]');
  if (changed) changed.textContent = String(countBits(summaryDev.changed));
  const heroMode = document.querySelector('[data-device-mode]');
  if (heroMode) {
    const mode = summaryDev.manual_mask ? '手动' : (summaryDev.climate_enabled ? '自动' : '固定');
    heroMode.textContent = mode;
    heroMode.className = 'badge ' + (summaryDev.manual_mask ? 'manual' : 'auto');
  }
  for (let u = 1; u <= summaryDev.u_count; u++) {
    const cell = document.querySelector('[data-u-cell="' + u + '"]');
    if (!cell) continue;
    cell.classList.toggle('present', bitSet(summaryDev.present, u - 1));
    cell.classList.toggle('changed', bitSet(summaryDev.changed, u - 1));
  }
}

/* ------------------------------------------------------------ mutations */

async function saveInfoForm() {
  const detail = state.device;
  if (!detail) return;
  const id = detail.snapshot.id;
  const value = (sel) => qs(sel).value;
  const num = (sel) => {
    const n = Number(value(sel));
    if (!Number.isFinite(n)) throw new Error('请输入合法数字');
    return n;
  };
  const hex = (sel) => {
    const n = parseInt(value(sel), 16);
    if (!Number.isFinite(n)) throw new Error('请输入合法十六进制');
    return n & 0xffff;
  };
  try {
    const body = {
      name: value('#f-name').trim(),
      esn: value('#f-esn').trim(),
      model: hex('#f-model'),
      baud_code: num('#f-baud'),
      soft_version: hex('#f-soft'),
      hard_version: hex('#f-hard'),
      u_count: num('#f-ucount'),
      tcp_unit_id: num('#f-tcpunit'),
      changed_hold_ms: num('#f-hold'),
      adopt_address: qs('#f-adopt').checked,
      strict: qs('#f-strict').checked,
    };
    const result = await api('/api/devices/' + encodeURIComponent(id), { method: 'PATCH', body });
    applyDevice(result, '设备信息已保存');
  } catch (err) {
    reportError(err);
  }
}

async function addBindingForm() {
  const detail = state.device;
  if (!detail) return;
  const port = qs('#binding-port') ? qs('#binding-port').value : '';
  const unit = Number(qs('#binding-unit') ? qs('#binding-unit').value : 0);
  if (!port) {
    toast('没有可用的串口端口', 'warn');
    return;
  }
  try {
    const result = await api('/api/bindings', {
      method: 'POST',
      body: { device: detail.snapshot.id, port, unit_id: unit },
    });
    applyDevice(result, '绑定已添加');
  } catch (err) {
    reportError(err);
  }
}

async function removeBinding(port, unit) {
  const detail = state.device;
  if (!detail) return;
  try {
    const result = await api('/api/bindings', {
      method: 'DELETE',
      body: { device: detail.snapshot.id, port, unit_id: unit },
    });
    applyDevice(result, '绑定已移除');
  } catch (err) {
    reportError(err);
  }
}

async function pulseChanged(u) {
  const detail = state.device;
  if (!detail) return;
  try {
    const result = await api('/api/devices/' + encodeURIComponent(detail.snapshot.id) + '/pulse-changed', {
      method: 'POST',
      body: { u },
    });
    applyDevice(result, u ? ('U' + u + ' 变化位已触发') : '全部变化位已触发');
  } catch (err) {
    reportError(err);
  }
}

async function deleteDevice() {
  const detail = state.device;
  if (!detail) return;
  const ok = await confirmDialog('删除设备', '确定删除「' + (detail.snapshot.name || detail.snapshot.id) + '」及其全部绑定吗？此操作会写入 state.json。', '删除');
  if (!ok) return;
  try {
    await api('/api/devices/' + encodeURIComponent(detail.snapshot.id), { method: 'DELETE' });
    toast('设备已删除');
    refreshSummary();
    navigate('#/overview');
  } catch (err) {
    reportError(err);
  }
}

function climateParamsFromForm() {
  const num = (sel) => {
    const n = Number(qs(sel).value);
    if (!Number.isFinite(n)) throw new Error('温湿度参数含非法数字');
    return n;
  };
  return {
    enabled: qs('#c-enabled').checked,
    timezone: qs('#c-timezone').value.trim(),
    time_scale: num('#c-timescale'),
    update_ms: num('#c-update'),
    temperature: {
      mean: num('#c-tmean'),
      amplitude: num('#c-tamp'),
      peak_hour: num('#c-tpeak'),
      harmonic2_amplitude: num('#c-th2'),
      min: num('#c-tmin'),
      max: num('#c-tmax'),
    },
    humidity: {
      mean: num('#c-hmean'),
      temp_coupling: num('#c-hcouple'),
      amplitude: num('#c-hamp'),
      min: num('#c-hmin'),
      max: num('#c-hmax'),
    },
    zone_offsets: {
      top: { temp: num('#c-zt-top'), humi: num('#c-zh-top'), lag_h: num('#c-zl-top') },
      mid: { temp: num('#c-zt-mid'), humi: num('#c-zh-mid'), lag_h: num('#c-zl-mid') },
      bottom: { temp: num('#c-zt-bottom'), humi: num('#c-zh-bottom'), lag_h: num('#c-zl-bottom') },
    },
    noise: {
      temp_tau_ms: num('#c-ntau'),
      temp_sigma: num('#c-nsigma'),
      humi_tau_ms: num('#c-htau'),
      humi_sigma: num('#c-hsigma'),
    },
    fixed: [num('#c-fixed-temp'), num('#c-fixed-humi')],
  };
}

async function saveClimateForm() {
  const detail = state.device;
  if (!detail) return;
  try {
    const params = climateParamsFromForm();
    const result = await api('/api/devices/' + encodeURIComponent(detail.snapshot.id) + '/climate', {
      method: 'PUT',
      body: params,
    });
    applyDevice(result, '温湿度参数已保存');
  } catch (err) {
    reportError(err);
  }
}

/* ------------------------------------------------------------------ modals */

let modalKeyHandler = null;
let modalOnClose = null;

function openModal(opts) {
  closeModal();
  modalOnClose = opts.onClose || null;
  document.body.classList.add('modal-open');
  const root = document.getElementById('modal-root');
  const actions = (opts.actions || []).map((a) =>
    '<button class="btn ' + (a.className || '') + '" type="button" id="' + a.id + '">' + esc(a.label) + '</button>').join('');
  root.innerHTML = '<div class="modal-backdrop" data-backdrop>' +
    '<div class="modal" role="dialog" aria-modal="true" aria-label="' + esc(opts.title) + '">' +
      '<div class="modal-head"><h3>' + esc(opts.title) + '</h3><div class="spacer"></div>' +
        '<button class="icon-btn" type="button" id="modal-close" aria-label="关闭">✕</button></div>' +
      '<div class="modal-body">' + (opts.body || '') + '</div>' +
      (actions ? '<div class="modal-actions">' + actions + '</div>' : '') +
    '</div></div>';
  const backdrop = root.querySelector('[data-backdrop]');
  backdrop.addEventListener('click', (ev) => {
    if (ev.target === backdrop) closeModal();
  });
  const close = root.querySelector('#modal-close');
  if (close) close.addEventListener('click', closeModal);
  modalKeyHandler = (ev) => {
    if (ev.key === 'Escape') closeModal();
  };
  document.addEventListener('keydown', modalKeyHandler);
  if (opts.onMount) opts.onMount(root);
  const focusable = root.querySelector('.modal-body input, .modal-body select, .modal-body textarea') ||
    root.querySelector('.modal-actions .btn.primary') || close;
  if (focusable) focusable.focus();
}

function closeModal() {
  const cb = modalOnClose;
  modalOnClose = null;
  document.body.classList.remove('modal-open');
  const root = document.getElementById('modal-root');
  if (root) root.innerHTML = '';
  if (modalKeyHandler) {
    document.removeEventListener('keydown', modalKeyHandler);
    modalKeyHandler = null;
  }
  if (cb) cb();
}

function openTokenModal() {
  if (document.getElementById('token-input')) return;
  openModal({
    title: '访问令牌',
    body: '<div class="subtle">若服务端配置了 <code>server.web.token</code>，请在此填写相同的令牌。' +
      '令牌仅保存在浏览器 localStorage，不会写回服务端。</div>' +
      '<div class="field"><label for="token-input">令牌</label>' +
      '<input id="token-input" type="password" autocomplete="off" value="' + esc(state.token) + '" placeholder="留空表示无令牌"></div>',
    actions: [
      { id: 'token-clear', label: '清除', className: 'ghost' },
      { id: 'token-save', label: '保存并重连', className: 'primary' },
    ],
    onMount: () => {
      document.getElementById('token-clear').addEventListener('click', () => {
        state.token = '';
        localStorage.removeItem('iadu-sim-token');
        closeModal();
        connectSSE();
        refreshSummary();
        route();
        toast('令牌已清除');
      });
      document.getElementById('token-save').addEventListener('click', () => {
        const value = document.getElementById('token-input').value.trim();
        state.token = value;
        if (value) localStorage.setItem('iadu-sim-token', value);
        else localStorage.removeItem('iadu-sim-token');
        closeModal();
        connectSSE();
        refreshSummary();
        route();
        toast('令牌已更新');
      });
    },
  });
}

function navigate(hash) {
  if (location.hash === hash) route();
  else location.hash = hash;
}

function confirmDialog(title, message, confirmLabel) {
  return new Promise((resolve) => {
    let settled = false;
    const finish = (value) => {
      if (settled) return;
      settled = true;
      closeModal();
      resolve(value);
    };
    openModal({
      title,
      body: '<div class="subtle">' + esc(message) + '</div>',
      actions: [
        { id: 'confirm-cancel', label: '取消', className: 'ghost' },
        { id: 'confirm-ok', label: confirmLabel || '确认', className: 'danger' },
      ],
      onClose: () => {
        if (!settled) {
          settled = true;
          resolve(false);
        }
      },
      onMount: () => {
        document.getElementById('confirm-cancel').addEventListener('click', () => finish(false));
        document.getElementById('confirm-ok').addEventListener('click', () => finish(true));
      },
    });
  });
}

function openAddDeviceModal() {
  const ports = state.summary ? state.summary.ports : [];
  const portOptions = ports.map((p) => '<option value="' + esc(p.name) + '">' + esc(p.name) + '</option>').join('');
  openModal({
    title: '添加模拟设备',
    body: '<div class="info-grid">' +
      '<div class="field"><label for="new-id">设备 ID</label><input id="new-id" placeholder="iadu-2" autocomplete="off"></div>' +
      '<div class="field"><label for="new-name">显示名称</label><input id="new-name" placeholder="机柜B 42U"></div>' +
      '<div class="field"><label for="new-u">U 位长度</label><input id="new-u" type="number" min="1" max="42" value="42"></div>' +
      '<div class="field"><label for="new-esn">ESN（可空）</label><input id="new-esn" maxlength="20" placeholder="自动生成"></div>' +
      '<div class="field"><label for="new-occ">在位 U 位</label><input id="new-occ" value="1-10,20" placeholder="all / 1-10,20"></div>' +
      '<div class="field"><label for="new-tcp">TCP unit（0=自动）</label><input id="new-tcp" type="number" min="0" max="247" value="0"></div>' +
      '<div class="field"><label for="new-port">串口绑定（可空）</label><select id="new-port"><option value="">不绑定</option>' + portOptions + '</select></div>' +
      '<div class="field"><label for="new-unit">从站地址</label><input id="new-unit" type="number" min="1" max="247" value="1"></div>' +
    '</div>' +
    '<div class="section-note">新设备默认启用动态温湿度；可在设备详情中调整。</div>',
    actions: [
      { id: 'new-cancel', label: '取消', className: 'ghost' },
      { id: 'new-create', label: '创建设备', className: 'primary' },
    ],
    onMount: () => {
      document.getElementById('new-cancel').addEventListener('click', closeModal);
      document.getElementById('new-create').addEventListener('click', async () => {
        const id = document.getElementById('new-id').value.trim();
        if (!id) {
          toast('设备 ID 不能为空', 'warn');
          return;
        }
        const body = {
          id,
          name: document.getElementById('new-name').value.trim(),
          u_count: Number(document.getElementById('new-u').value),
          esn: document.getElementById('new-esn').value.trim(),
          occupancy: document.getElementById('new-occ').value.trim(),
          tcp_unit_id: Number(document.getElementById('new-tcp').value),
        };
        const port = document.getElementById('new-port').value;
        if (port) {
          body.bindings = [{ port, unit_id: Number(document.getElementById('new-unit').value) }];
        }
        try {
          const detail = await api('/api/devices', { method: 'POST', body });
          closeModal();
          toast('设备已创建');
          refreshSummary();
          navigate('#/devices/' + encodeURIComponent(detail.snapshot.id));
        } catch (err) {
          reportError(err);
        }
      });
    },
  });
}

function openTagModal(u) {
  const detail = state.device;
  if (!detail) return;
  const snap = detail.snapshot;
  const tag = snap.tags && snap.tags[u - 1] ? snap.tags[u - 1] : '';
  const present = bitSet(detail.status.present, u - 1);
  openModal({
    title: 'U' + String(u).padStart(2, '0') + ' · 标签与在位',
    body: '<div class="field"><label for="tag-value">标签（16 位十六进制 / 8 字节）</label>' +
      '<input id="tag-value" value="' + esc(tag) + '" placeholder="E200000000000001" autocomplete="off"></div>' +
      '<label class="checkline"><input id="tag-present" type="checkbox"' + (present ? ' checked' : '') + '> 该 U 位有标签/设备</label>' +
      '<div class="section-note">保存后同时更新 RTU 输入寄存器与 TCP 影子区。清空标签会写入 8 个零字节。</div>',
    actions: [
      { id: 'tag-pulse', label: '触发变化位', className: 'ghost' },
      { id: 'tag-cancel', label: '取消', className: 'ghost' },
      { id: 'tag-save', label: '保存', className: 'primary' },
    ],
    onMount: () => {
      document.getElementById('tag-cancel').addEventListener('click', closeModal);
      document.getElementById('tag-pulse').addEventListener('click', async () => {
        closeModal();
        await pulseChanged(u);
      });
      document.getElementById('tag-save').addEventListener('click', async () => {
        try {
          const body = {
            tag: document.getElementById('tag-value').value.trim(),
            present: document.getElementById('tag-present').checked,
          };
          const result = await api('/api/devices/' + encodeURIComponent(snap.id) + '/tags/' + u, {
            method: 'PUT', body,
          });
          closeModal();
          applyDevice(result, 'U' + u + ' 已保存');
        } catch (err) {
          reportError(err);
        }
      });
    },
  });
}

function openManualModal(zone) {
  const detail = state.device;
  if (!detail) return;
  const base = detail.th && detail.th[zone] ? detail.th[zone] : { temp: 25, humi: 50 };
  const zoneOptions = ZONES.map((z) =>
    '<option value="' + z + '"' + (z === zone ? ' selected' : '') + '>' + ZONE_LABEL[z] + '</option>').join('') +
    '<option value="all">全部三区</option>';
  openModal({
    title: '手动覆盖温湿度',
    body: '<div class="info-grid">' +
      '<div class="field"><label for="manual-zone">区域</label><select id="manual-zone">' + zoneOptions + '</select></div>' +
      '<div class="field"><label for="manual-temp">温度 (℃)</label><input id="manual-temp" type="number" step="0.5" value="' + fmtNum(base.temp) + '"></div>' +
      '<div class="field"><label for="manual-humi">湿度 (%)</label><input id="manual-humi" type="number" step="0.5" value="' + fmtNum(base.humi) + '"></div>' +
      '<div class="field"><label for="manual-duration">持续秒数（0=永久）</label><input id="manual-duration" type="number" min="0" value="300"></div>' +
    '</div>' +
    '<div class="section-note">覆盖期间自动曲线暂停；到期或点击「恢复自动」后继续。</div>',
    actions: [
      { id: 'manual-cancel', label: '取消', className: 'ghost' },
      { id: 'manual-save', label: '应用覆盖', className: 'primary' },
    ],
    onMount: () => {
      document.getElementById('manual-cancel').addEventListener('click', closeModal);
      document.getElementById('manual-save').addEventListener('click', async () => {
        try {
          const body = {
            zone: document.getElementById('manual-zone').value,
            temp: Number(document.getElementById('manual-temp').value),
            humi: Number(document.getElementById('manual-humi').value),
            duration_s: Number(document.getElementById('manual-duration').value),
          };
          const result = await api('/api/devices/' + encodeURIComponent(detail.snapshot.id) + '/climate/override', {
            method: 'PUT', body,
          });
          closeModal();
          applyDevice(result, '手动覆盖已应用');
        } catch (err) {
          reportError(err);
        }
      });
    },
  });
}

function openLedModal(u) {
  const detail = state.device;
  if (!detail) return;
  const value = detail.snapshot.led && detail.snapshot.led[u - 1] ? detail.snapshot.led[u - 1] : 0;
  openModal({
    title: 'U' + String(u).padStart(2, '0') + ' · LED 值',
    body: '<div class="field"><label for="led-value">16 位值（hex）</label>' +
      '<input id="led-value" value="' + hex4(value) + '" autocomplete="off"></div>' +
      '<div class="section-note">模拟器原样保存并回读该值，不解释颜色编码。</div>',
    actions: [
      { id: 'led-cancel', label: '取消', className: 'ghost' },
      { id: 'led-save', label: '保存', className: 'primary' },
    ],
    onMount: () => {
      document.getElementById('led-cancel').addEventListener('click', closeModal);
      document.getElementById('led-save').addEventListener('click', async () => {
        const n = parseInt(document.getElementById('led-value').value, 16);
        if (!Number.isFinite(n)) {
          toast('请输入合法十六进制值', 'warn');
          return;
        }
        try {
          const result = await api('/api/devices/' + encodeURIComponent(detail.snapshot.id) + '/led/' + u, {
            method: 'PUT', body: { value: n & 0xffff },
          });
          closeModal();
          applyDevice(result, 'LED 已更新');
        } catch (err) {
          reportError(err);
        }
      });
    },
  });
}

/* ------------------------------------------------------------------ ports */

function portSignature(summary) {
  const binds = [];
  for (const d of summary.devices) {
    for (const b of d.bindings || []) binds.push(d.id + ':' + b.port + ':' + b.unit_id);
  }
  return summary.ports.map((p) => p.name).join(',') + '|' + binds.join(',');
}

function renderPorts() {
  if (!state.summary) return;
  const s = state.summary;
  const ports = s.ports || [];
  const bindMap = new Map();
  for (const d of s.devices) {
    for (const b of d.bindings || []) {
      if (!bindMap.has(b.port)) bindMap.set(b.port, []);
      bindMap.get(b.port).push(d.name + ' · unit ' + b.unit_id);
    }
  }
  const totalRx = ports.reduce((a, p) => a + (p.rx || 0), 0) + (s.mbap ? s.mbap.rx || 0 : 0);
  const totalTx = ports.reduce((a, p) => a + (p.tx || 0), 0) + (s.mbap ? s.mbap.tx || 0 : 0);
  const crc = ports.reduce((a, p) => a + (p.crc_errors || 0), 0);
  const main = document.getElementById('main');
  main.innerHTML = '<div class="content-inner">' +
    '<div class="tiles">' +
      tileHTML('pt-ports', 'RTU 端口', String(ports.length), '虚拟 pty / 真实串口') +
      tileHTML('pt-online', '在线', String(onlinePortCount()), '最近有采集访问') +
      tileHTML('pt-frames', '收发帧', String(totalRx + totalTx), 'RX ' + totalRx + ' · TX ' + totalTx) +
      tileHTML('pt-errors', 'CRC 错误', String(crc), '重同步计数') +
    '</div>' +
    '<div class="port-grid">' + ports.map((p) => portCardHTML(p, bindMap.get(p.name) || [])).join('') + mbapCardHTML(s) + '</div>' +
  '</div>';
  qsa('[data-copy]', main).forEach((btn) => {
    btn.addEventListener('click', () => copyText(btn.dataset.copy));
  });
  state.portsSig = portSignature(s);
  state.renderedRoute = 'ports';
}

function portCardHTML(p, bound) {
  const online = p.connected;
  return '<div class="port-card" data-port-card="' + esc(p.name) + '">' +
    '<div class="port-head"><span class="dot ' + (online ? 'online' : 'idle') + '" data-port-dot></span>' +
      '<h3>' + esc(p.name) + '</h3><span class="badge">' + esc(p.transport) + '</span>' +
      '<div class="spacer"></div><span class="badge ' + (online ? 'ok' : '') + '" data-port-state>' + (online ? '在线' : '空闲') + '</span></div>' +
    '<div class="path"><span>链路</span><span class="mono">' + esc(p.link || '—') + '</span>' +
      (p.link ? '<button class="icon-btn" type="button" data-copy="' + esc(p.link) + '">复制</button>' : '') + '</div>' +
    (p.slave_path ? '<div class="path"><span>pty</span><span class="mono">' + esc(p.slave_path) + '</span>' +
      '<button class="icon-btn" type="button" data-copy="' + esc(p.slave_path) + '">复制</button></div>' : '') +
    '<div class="path"><span>绑定</span><span>' + (bound.length ? bound.map(esc).join('，') : '未绑定设备') + '</span></div>' +
    '<div class="stat-grid">' +
      '<div class="stat"><div class="k">RX</div><div class="v" data-port-rx>' + (p.rx || 0) + '</div></div>' +
      '<div class="stat"><div class="k">TX</div><div class="v" data-port-tx>' + (p.tx || 0) + '</div></div>' +
      '<div class="stat"><div class="k">字节</div><div class="v" data-port-bytes>' + ((p.rx_bytes || 0) + (p.tx_bytes || 0)) + '</div></div>' +
      '<div class="stat"><div class="k">CRC 错</div><div class="v" data-port-crc>' + (p.crc_errors || 0) + '</div></div>' +
    '</div>' +
    '<div class="path"><span>最近 RX</span><span data-port-lastrx>' + relTime(p.last_rx) + '</span>' +
      '<span>·</span><span>最近 TX</span><span data-port-lasttx>' + relTime(p.last_tx) + '</span></div>' +
  '</div>';
}

function mbapCardHTML(s) {
  const m = s.mbap;
  if (!m) {
    return '<div class="port-card"><div class="port-head"><span class="dot idle"></span><h3>MODBUS TCP</h3>' +
      '<span class="badge">调整服务</span></div><div class="section-note">本次运行未启用 Modbus TCP 调整服务。</div></div>';
  }
  return '<div class="port-card"><div class="port-head"><span class="dot online"></span><h3>MODBUS TCP</h3>' +
    '<span class="badge tcp">调整服务</span><div class="spacer"></div>' +
    '<span class="badge ok" data-mbap-conns>' + (m.active_conns || 0) + ' 连接</span></div>' +
    '<div class="path"><span>监听</span><span class="mono">' + esc(displayListen(m.listen)) + '</span>' +
      '<button class="icon-btn" type="button" data-copy="' + esc(m.listen || '') + '">复制</button></div>' +
    '<div class="stat-grid">' +
      '<div class="stat"><div class="k">RX</div><div class="v" data-mbap-rx>' + (m.rx || 0) + '</div></div>' +
      '<div class="stat"><div class="k">TX</div><div class="v" data-mbap-tx>' + (m.tx || 0) + '</div></div>' +
      '<div class="stat"><div class="k">字节</div><div class="v" data-mbap-bytes>' + ((m.rx_bytes || 0) + (m.tx_bytes || 0)) + '</div></div>' +
      '<div class="stat"><div class="k">错误</div><div class="v" data-mbap-errors>' + (m.errors || 0) + '</div></div>' +
    '</div>' +
    '<div class="section-note">影子区从 0x8000 开始；<code>mbpoll -m tcp -p ' + esc((m.listen || ':15020').split(':').pop()) + ' -a 1 -0 -r 32768 -c 6 -t 3</code></div>' +
  '</div>';
}

function updatePorts() {
  if (!state.summary || state.renderedRoute !== 'ports') return;
  if (portSignature(state.summary) !== state.portsSig) {
    renderPorts();
    return;
  }
  const s = state.summary;
  for (const p of s.ports) {
    const card = Array.from(document.querySelectorAll('[data-port-card]'))
      .find((el) => el.dataset.portCard === p.name);
    if (!card) continue;
    const dot = card.querySelector('[data-port-dot]');
    if (dot) dot.className = 'dot ' + (p.connected ? 'online' : 'idle');
    const stateBadge = card.querySelector('[data-port-state]');
    if (stateBadge) {
      stateBadge.textContent = p.connected ? '在线' : '空闲';
      stateBadge.className = 'badge ' + (p.connected ? 'ok' : '');
    }
    const set = (sel, value) => {
      const el = card.querySelector(sel);
      if (el) el.textContent = value;
    };
    set('[data-port-rx]', String(p.rx || 0));
    set('[data-port-tx]', String(p.tx || 0));
    set('[data-port-bytes]', String((p.rx_bytes || 0) + (p.tx_bytes || 0)));
    set('[data-port-crc]', String(p.crc_errors || 0));
    set('[data-port-lastrx]', relTime(p.last_rx));
    set('[data-port-lasttx]', relTime(p.last_tx));
  }
  const totalRx = s.ports.reduce((a, p) => a + (p.rx || 0), 0) + (s.mbap ? s.mbap.rx || 0 : 0);
  const totalTx = s.ports.reduce((a, p) => a + (p.tx || 0), 0) + (s.mbap ? s.mbap.tx || 0 : 0);
  const crc = s.ports.reduce((a, p) => a + (p.crc_errors || 0), 0);
  const setText = (id, value) => {
    const el = document.getElementById(id);
    if (el) el.textContent = value;
  };
  setText('pt-ports', String(s.ports.length));
  setText('pt-online', String(onlinePortCount()));
  setText('pt-frames', String(totalRx + totalTx));
  setText('pt-frames-sub', 'RX ' + totalRx + ' · TX ' + totalTx);
  setText('pt-errors', String(crc));
  if (s.mbap) {
    const setAttrText = (attr, value) => {
      const el = document.querySelector('[' + attr + ']');
      if (el) el.textContent = value;
    };
    setAttrText('data-mbap-rx', String(s.mbap.rx || 0));
    setAttrText('data-mbap-tx', String(s.mbap.tx || 0));
    setAttrText('data-mbap-bytes', String((s.mbap.rx_bytes || 0) + (s.mbap.tx_bytes || 0)));
    setAttrText('data-mbap-errors', String(s.mbap.errors || 0));
    const conns = document.querySelector('[data-mbap-conns]');
    if (conns) conns.textContent = (s.mbap.active_conns || 0) + ' 连接';
  }
}

/* ------------------------------------------------------------------- logs */

async function renderLogs() {
  const s = state.summary;
  const ports = s ? s.ports.map((p) => p.name) : [];
  const options = ['<option value="all">全部来源</option>']
    .concat(ports.map((n) => '<option value="' + esc(n) + '">' + esc(n) + '</option>'))
    .concat('<option value="tcp">Modbus TCP</option>').join('');
  setMain('<div class="content-inner">' +
    '<div class="toolbar">' +
      '<label>来源 <select id="log-port" aria-label="来源过滤">' + options + '</select></label>' +
      '<label>方向 <select id="log-dir" aria-label="方向过滤"><option value="all">全部</option><option value="rx">RX</option><option value="tx">TX</option></select></label>' +
      '<button class="btn small ghost" type="button" id="log-pause">暂停</button>' +
      '<button class="btn small ghost" type="button" id="log-clear">清空显示</button>' +
      '<label class="checkline"><input type="checkbox" id="log-autoscroll" checked> 自动滚动</label>' +
      '<div class="spacer"></div><span class="scroll-hint" id="log-count">0 条</span>' +
      '<span class="paused-note" id="log-paused" hidden></span>' +
    '</div>' +
    '<div class="log-wrap" id="log-wrap"><table class="log-table">' +
      '<thead><tr><th>时间</th><th>来源</th><th>方向</th><th>单元</th><th>FC</th><th>报文</th><th>备注</th></tr></thead>' +
      '<tbody id="log-body"></tbody></table></div>' +
  '</div>');
  state.logFilter = { port: 'all', dir: 'all' };
  state.logPaused = false;
  state.logBuffer = [];
  state.logReady = false;
  state.renderedRoute = 'logs';
  bindLogControls();
  renderLogTable();
  try {
    const data = await api('/api/frames?limit=300');
    state.logs = Array.isArray(data.frames) ? data.frames.slice() : [];
    state.logReady = true;
    renderLogTable();
    scrollLogs();
  } catch (err) {
    reportError(err);
  }
}

function bindLogControls() {
  const portSel = document.getElementById('log-port');
  const dirSel = document.getElementById('log-dir');
  const pauseBtn = document.getElementById('log-pause');
  const clearBtn = document.getElementById('log-clear');
  const auto = document.getElementById('log-autoscroll');
  if (portSel) portSel.addEventListener('change', () => {
    state.logFilter.port = portSel.value;
    renderLogTable();
  });
  if (dirSel) dirSel.addEventListener('change', () => {
    state.logFilter.dir = dirSel.value;
    renderLogTable();
  });
  if (pauseBtn) pauseBtn.addEventListener('click', () => {
    state.logPaused = !state.logPaused;
    pauseBtn.textContent = state.logPaused ? '继续' : '暂停';
    pauseBtn.classList.toggle('amber', state.logPaused);
    const note = document.getElementById('log-paused');
    if (note) note.hidden = !state.logPaused;
    if (!state.logPaused) {
      if (state.logBuffer.length) {
        state.logs = state.logs.concat(state.logBuffer);
        state.logBuffer = [];
        if (state.logs.length > 800) state.logs = state.logs.slice(-800);
        renderLogTable();
        scrollLogs();
      }
    }
    updateLogPausedNote();
  });
  if (clearBtn) clearBtn.addEventListener('click', () => {
    state.logs = [];
    state.logBuffer = [];
    renderLogTable();
  });
  if (auto) auto.addEventListener('change', () => {
    state.logAutoScroll = auto.checked;
  });
  const tbody = document.getElementById('log-body');
  if (tbody) tbody.addEventListener('click', (ev) => {
    const btn = ev.target.closest('[data-copy]');
    if (btn) copyText(btn.dataset.copy);
  });
}

function logMatches(entry) {
  if (state.logFilter.port !== 'all' && entry.port !== state.logFilter.port) return false;
  if (state.logFilter.dir !== 'all' && entry.dir !== state.logFilter.dir) return false;
  return true;
}

function logRowHTML(entry) {
  const isException = entry.dir === 'tx' && Number(entry.fc) >= 0x80;
  const bad = entry.ok === false || isException;
  const cls = (entry.dir === 'rx' ? 'rx' : 'tx') + (bad ? ' bad' : '');
  const time = parseTime(entry.time);
  const source = entry.port || entry.transport || '—';
  const note = entry.ok === false
    ? (entry.error || '错误')
    : (isException ? ('异常 0x' + hex2(entry.fc)) : (entry.transport === 'mbap' ? 'MBAP' : 'RTU'));
  return '<tr class="' + cls + '">' +
    '<td class="mono">' + fmtClock(time) + '</td>' +
    '<td>' + esc(source) + '</td>' +
    '<td class="dir">' + (entry.dir === 'rx' ? 'RX' : 'TX') + '</td>' +
    '<td>' + (entry.unit ? hex2(entry.unit) : '—') + '</td>' +
    '<td>' + (entry.fc ? hex2(entry.fc) : '—') + '</td>' +
    '<td><div class="log-cell"><span class="log-hex" title="' + esc(entry.hex || '') + '">' + esc(entry.hex || '') + '</span>' +
      '<button class="icon-btn" type="button" data-copy="' + esc(entry.hex || '') + '">复制</button></div></td>' +
    '<td class="log-note">' + esc(note) + '</td>' +
  '</tr>';
}

function renderLogTable() {
  const body = document.getElementById('log-body');
  if (!body) return;
  const filtered = state.logs.filter(logMatches).slice(-800);
  body.innerHTML = filtered.map(logRowHTML).join('');
  const count = document.getElementById('log-count');
  if (count) count.textContent = filtered.length + ' 条';
  updateLogPausedNote();
}

function appendLogs(entries) {
  if (!Array.isArray(entries) || entries.length === 0) return;
  const lastSeq = state.logs.length ? Number(state.logs[state.logs.length - 1].seq || 0) : 0;
  let fresh = entries.filter((e) => Number(e.seq || 0) > lastSeq);
  if (fresh.length === 0) return;
  if (state.logPaused) {
    state.logBuffer = state.logBuffer.concat(fresh);
    if (state.logBuffer.length > 500) state.logBuffer = state.logBuffer.slice(-500);
    updateLogPausedNote();
    return;
  }
  state.logs = state.logs.concat(fresh);
  if (state.logs.length > 800) state.logs = state.logs.slice(-800);
  if (state.route.name !== 'logs' || !state.logReady) return;
  const body = document.getElementById('log-body');
  if (!body) return;
  const matching = fresh.filter(logMatches);
  if (matching.length) {
    const rows = matching.map(logRowHTML).join('');
    body.insertAdjacentHTML('beforeend', rows);
    while (body.children.length > 800) body.removeChild(body.firstElementChild);
    const count = document.getElementById('log-count');
    if (count) count.textContent = body.children.length + ' 条';
    scrollLogs();
  }
}

function scrollLogs() {
  if (!state.logAutoScroll) return;
  const wrap = document.getElementById('log-wrap');
  if (wrap) wrap.scrollTop = wrap.scrollHeight;
}

function updateLogPausedNote() {
  const note = document.getElementById('log-paused');
  if (!note) return;
  if (state.logPaused) {
    note.hidden = false;
    note.textContent = '已暂停 · 缓冲区 ' + state.logBuffer.length + ' 条';
  } else {
    note.hidden = true;
    note.textContent = '';
  }
}

/* ---------------------------------------------------------------- settings */

function renderSettings() {
  const s = state.summary;
  if (!s) {
    setMain('<div class="loading">正在加载系统信息…</div>');
    return;
  }
  setMain('<div class="content-inner">' +
    '<div class="settings-grid">' +
      '<div class="card"><div class="card-head"><div class="card-title">系统</div></div>' +
        '<div class="kv"><span class="k">版本</span><span class="v">' + esc(s.version) + ' · ' + esc(s.commit) + '</span></div>' +
        '<div class="kv"><span class="k">运行时长</span><span class="v" id="set-uptime">' + fmtUptime(s.uptime_s) + '</span></div>' +
        '<div class="kv"><span class="k">Web 监听</span><span class="v">' + esc(s.web_listen) + '</span></div>' +
        '<div class="kv"><span class="k">Modbus TCP</span><span class="v">' + esc(s.tcp_listen ? displayListen(s.tcp_listen) : '未启用') + '</span></div>' +
        '<div class="kv"><span class="k">配置文件</span><span class="v">' + esc(s.config_path || '—') + '</span></div>' +
        '<div class="kv"><span class="k">状态文件</span><span class="v">' + esc(s.state_file || '—') + '</span></div>' +
      '</div>' +
      '<div class="card"><div class="card-head"><div class="card-title">访问令牌</div></div>' +
        '<div class="subtle">服务端令牌来自 <code>server.web.token</code> 或环境变量 <code>IADU_SIM_TOKEN</code>。' +
          '浏览器令牌只保存在 localStorage。</div>' +
        '<div class="field mt-12"><label for="set-token">当前浏览器令牌</label>' +
          '<input id="set-token" type="password" autocomplete="off" value="' + esc(state.token) + '" placeholder="无令牌"></div>' +
        '<div class="form-actions"><button class="btn ghost" type="button" id="set-token-clear">清除</button>' +
          '<button class="btn primary" type="button" id="set-token-save">保存并重连</button></div>' +
      '</div>' +
      '<div class="card"><div class="card-head"><div class="card-title">RTU 虚拟串口</div></div>' +
        '<ul class="note-list">' +
          s.ports.map((p) => '<li><code>' + esc(p.name) + '</code> → <code>' + esc(p.link || p.device || '—') + '</code>' +
            (p.slave_path ? ' → <code>' + esc(p.slave_path) + '</code>' : '') + '</li>').join('') +
          '<li>目标采集程序打开 <code>link</code> 指向的路径；模拟器重启后 pts 编号变化，采集程序需要重新打开。</li>' +
        '</ul>' +
      '</div>' +
      '<div class="card"><div class="card-head"><div class="card-title">配置与状态</div></div>' +
        '<ul class="note-list">' +
          '<li><code>config.yaml</code> 管理进程、端口和首次运行种子；<code>state.json</code> 是设备/绑定的运行时权威。</li>' +
          '<li>页面上的设备/绑定/数值修改会原子写入 state.json；CLI 写入只改内存。</li>' +
          '<li>重新按配置播种：<code>iadu-sim --import-config</code>；状态损坏救援：<code>--allow-corrupt-state</code>。</li>' +
          '<li>默认 Web 只监听 <code>127.0.0.1</code>；远程访问请改配置并设置 token。</li>' +
        '</ul>' +
      '</div>' +
      '<div class="card"><div class="card-head"><div class="card-title">调试命令</div></div>' +
        '<ul class="note-list">' +
          '<li><code>iadu-sim --selftest</code> 离线校验 CRC/帧/寄存器/pty。</li>' +
          '<li><code>mbpoll -m tcp -p 15020 -a 1 -0 -r 32768 -c 6 -t 3</code> 读影子区设备信息。</li>' +
          '<li><code>mbpoll -m tcp -p 15020 -a 1 -0 -r 32832 -c 3 -t 4</code> 读影子区温湿度。</li>' +
          '<li>实时帧日志与本页 SSE 使用同一环形缓冲（默认 2048 条）。</li>' +
        '</ul>' +
      '</div>' +
    '</div>' +
  '</div>');
  bindSettings();
  state.renderedRoute = 'settings';
}

function bindSettings() {
  const save = document.getElementById('set-token-save');
  const clear = document.getElementById('set-token-clear');
  if (save) save.addEventListener('click', () => {
    const value = document.getElementById('set-token').value.trim();
    state.token = value;
    if (value) localStorage.setItem('iadu-sim-token', value);
    else localStorage.removeItem('iadu-sim-token');
    connectSSE();
    refreshSummary();
    toast('令牌已更新');
  });
  if (clear) clear.addEventListener('click', () => {
    state.token = '';
    localStorage.removeItem('iadu-sim-token');
    const input = document.getElementById('set-token');
    if (input) input.value = '';
    connectSSE();
    refreshSummary();
    toast('令牌已清除');
  });
}

function updateSettingsDynamic() {
  if (state.route.name !== 'settings' || !state.summary) return;
  const uptime = document.getElementById('set-uptime');
  if (uptime) uptime.textContent = fmtUptime(state.summary.uptime_s);
}

/* -------------------------------------------------------------------- init */

async function refreshSummary() {
  try {
    const data = await api('/api/summary');
    applySummary(data, false);
  } catch (err) {
    if (err.status !== 401) {
      reportError(err);
      if (!state.summary) {
        setMain('<div class="content-inner"><div class="error-box">无法加载控制台数据：' + esc(err.message) + '</div></div>');
      }
    }
  }
}

function init() {
  renderShell();
  window.addEventListener('hashchange', route);
  if (!location.hash) history.replaceState(null, '', '#/overview');
  route();
  refreshSummary();
  // ?nosse=1 keeps the console usable behind proxies that buffer SSE and also
  // makes headless screenshot verification deterministic.
  const params = new URLSearchParams(location.search);
  if (!params.has('nosse')) {
    connectSSE();
  } else {
    state.sseState = 'offline';
    updateLinkStatus();
  }
  window.addEventListener('resize', debounce(() => {
    if (state.route.name === 'overview') updateOverview();
    if (state.route.name === 'device') updateDeviceDynamic();
  }, 150));
}

if (document.readyState === 'loading') {
  document.addEventListener('DOMContentLoaded', init);
} else {
  init();
}
