// Kai Desktop frontend — same UI as `kai ui` but talks to the Go side
// via Wails bindings instead of HTTP. The bindings (Agents, Events,
// Header) are auto-generated from app.go's exported methods at build/dev
// time. See ../wailsjs/go/main/App.js after `wails dev` or `wails build`.

import { Agents, Events, Header } from '../wailsjs/go/main/App';

const POLL_AGENTS_MS = 2000;
const POLL_EVENTS_MS = 1000;

function fmtUptime(sec) {
  if (!sec || sec < 0) return '';
  const h = Math.floor(sec / 3600);
  const m = Math.floor((sec % 3600) / 60);
  if (h > 0) return `${h}h ${String(m).padStart(2, '0')}m`;
  return `${m}m`;
}

function fmtAgo(tsMs) {
  if (!tsMs) return '';
  const sec = Math.floor((Date.now() - tsMs) / 1000);
  if (sec < 5) return 'now';
  if (sec < 60) return `${sec}s ago`;
  if (sec < 3600) return `${Math.floor(sec / 60)}m ago`;
  return `${Math.floor(sec / 3600)}h ago`;
}

function sparklinePath(values, w, h) {
  if (!values || values.length === 0) return '';
  const max = Math.max(1, ...values);
  const stepX = w / (values.length - 1 || 1);
  const points = values.map((v, i) => {
    const x = i * stepX;
    const y = h - (v / max) * h;
    return `${x.toFixed(1)},${y.toFixed(1)}`;
  });
  return 'M ' + points.join(' L ');
}

function eventLabel(type) {
  const t = (type || '').toLowerCase();
  switch (t) {
    case 'push': return { label: 'PUSH', arrow: '→' };
    case 'recv': return { label: 'RECV', arrow: '←' };
    case 'merge': return { label: 'MERGE', arrow: '⇄' };
    case 'conflict': return { label: 'CONFLICT', arrow: '⚠' };
    case 'checkpoint': return { label: 'CHECKPOINT', arrow: '◆' };
    case 'skip': return { label: 'SKIP', arrow: '·' };
    default: return { label: t.toUpperCase(), arrow: '·' };
  }
}

function escapeHTML(s) {
  if (s == null) return '';
  return String(s)
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;');
}

async function loadHeader() {
  try {
    const d = await Header();
    const el = document.getElementById('header-summary');
    if (!d.agent_count) {
      el.textContent = 'no spawned agents';
    } else if (d.repo_count <= 1) {
      const repo = d.sole_repo || '—';
      el.textContent = `${d.agent_count} agent${d.agent_count === 1 ? '' : 's'} · ${repo}`;
    } else {
      el.textContent = `${d.agent_count} agents across ${d.repo_count} repos`;
    }
  } catch (e) { console.error(e); }
}

async function loadAgents() {
  let agents = [];
  try { agents = await Agents(); } catch (e) { console.error(e); }
  const grid = document.getElementById('agent-grid');
  const empty = document.getElementById('empty-state');
  if (!agents || agents.length === 0) {
    grid.innerHTML = '';
    empty.classList.remove('hidden');
    return;
  }
  empty.classList.add('hidden');
  grid.innerHTML = agents.map(renderAgentCard).join('');
}

function renderAgentCard(a) {
  const sparkW = 240, sparkH = 40;
  const path = sparklinePath(a.sparkline || [], sparkW, sparkH);
  const isActive = a.last_event_ts && (Date.now() - a.last_event_ts) < 300_000;
  const dotClass = isActive ? 'bg-green-500 pulse' : 'bg-white/20';
  const lastFile = a.last_file ? a.last_file : '—';
  return `
    <div class="rounded-xl border border-white/10 bg-white/[0.02] p-5 hover:border-white/20 transition-colors">
      <div class="flex items-start justify-between mb-3">
        <div class="flex items-center gap-2 min-w-0">
          <span class="w-2 h-2 rounded-full shrink-0" style="background:${a.color}"></span>
          <span class="font-semibold text-white/90 truncate">${escapeHTML(a.name || a.workspace || '?')}</span>
          ${a.source_repo ? `<span class="mono text-xs text-white/30 truncate">· ${escapeHTML(a.source_repo)}</span>` : ''}
        </div>
        <span class="w-1.5 h-1.5 rounded-full ${dotClass} shrink-0"></span>
      </div>
      <div class="mono text-sm text-white/80 truncate mb-1" title="${escapeHTML(lastFile)}">${escapeHTML(lastFile)}</div>
      <div class="text-sm text-white/30 mb-4 truncate">&nbsp;</div>
      <svg viewBox="0 0 ${sparkW} ${sparkH}" class="w-full h-10 mb-4" preserveAspectRatio="none">
        <path d="${path}" fill="none" stroke="${a.color}" stroke-width="1.5" stroke-linejoin="round" stroke-linecap="round" />
      </svg>
      <div class="flex items-center justify-between text-xs text-white/40">
        <div class="flex items-center gap-3 mono">
          <span title="checkpoints">◉ ${a.checkpoints ?? 0}</span>
          <span title="uptime">${fmtUptime(a.uptime_sec)}</span>
        </div>
        <span class="w-1.5 h-1.5 rounded-full ${dotClass}"></span>
      </div>
    </div>
  `;
}

async function loadEvents() {
  let events = [];
  try { events = await Events(); } catch (e) { console.error(e); }
  const feed = document.getElementById('event-feed');
  if (!events || events.length === 0) {
    feed.innerHTML = '<span class="text-xs text-white/30">no events yet</span>';
    return;
  }
  feed.innerHTML = events.slice(0, 30).map(renderEventChip).join('');
}

function renderEventChip(e) {
  const { label, arrow } = eventLabel(e.type);
  const bg = `${e.color}1a`;
  const border = `${e.color}55`;
  return `
    <div class="shrink-0 rounded-md border px-3 py-1.5 text-xs mono" style="background:${bg};border-color:${border}">
      <div class="flex items-center gap-1.5 font-semibold" style="color:${e.color}">
        <span>${arrow}</span><span>${label}</span>
      </div>
      <div class="text-[10px] text-white/40 mt-0.5">${fmtAgo(e.timestamp)}</div>
    </div>
  `;
}

loadHeader();
loadAgents();
loadEvents();
setInterval(loadHeader, POLL_AGENTS_MS);
setInterval(loadAgents, POLL_AGENTS_MS);
setInterval(loadEvents, POLL_EVENTS_MS);
