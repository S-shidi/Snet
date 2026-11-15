/* SPDX-License-Identifier: BSD-3-Clause */
/* ── Shared UI logic for SNET client ──────────────────────────────
 * This module is the single source of truth for the client UI.
 * Platform adapters (Desktop/Web/Android) provide a Backend implementation
 * and call init() to wire everything up.
 */
import { DEFAULT_SERVER, type Backend, type DaemonStatus, type GatewayInfo, type WireGuardExport, type NetInfoDetail, type PeersResp, type CreateResp, type JoinResp, type PeerDirect, type NetInfo, type NetNode, type RelayRoute } from "./types.js";
import { $, esc, SPIN, fmtBytes, onlineCount, toast, copyText, renderQR, loadQR, openModal, closeModal, confirmDialog, initTabs } from "./utils.js";
import { subnetCheck, subnetWidgetHTML, subnetWidgetInit } from "./subnet.js";

// Optional hook: a platform adapter (Android WebView) can call refresh from
// native code after a background daemon op completes, to reconcile the
// optimistic switch quickly instead of waiting for the next poll.
(globalThis as any).snetRefresh = () => refresh();

let backend: Backend;
let status: DaemonStatus | null = null;

/* ── Settings (localStorage) ──────────────────────────────────── */
const SETTINGS_KEY = "snet.settings";
const DEFAULT_SETTINGS = { server: DEFAULT_SERVER, wgport: 51820 };
type Settings = typeof DEFAULT_SETTINGS;

function loadSettings(): Settings {
  let s: Settings;
  try {
    s = { ...DEFAULT_SETTINGS, ...JSON.parse(localStorage.getItem(SETTINGS_KEY) || "{}") };
  } catch {
    s = { ...DEFAULT_SETTINGS };
  }
  return s;
}
function saveSettings(s: Settings) {
  localStorage.setItem(SETTINGS_KEY, JSON.stringify(s));
}
function isOfficialServer(addr: string): boolean {
  const v = String(addr || "").trim().replace(/\/+$/, "");
  return !v || v === DEFAULT_SERVER.replace(/\/+$/, "");
}

function currentServer(): string {
  const configured = String(status?.serverAddr || loadSettings().server || "").trim();
  return isOfficialServer(configured) ? "" : configured;
}

/* ── Refresh ──────────────────────────────────────────────────── */
let refreshing = false;
let refreshAgain = false;
let desktopDaemon = { running: false, starting: false, error: "" };
let desktopPollTimer: number | undefined;
let detailNid: string | null = null;
let detailPendingId: string | null = null;
let lastNetHTML = "";
let lastEmptyHTML = "";
let lastTabsHTML = "";
async function refreshAfterDeviceChange() {
  // Peer/owner caches have 10s TTLs; bypass them so a rename or IP edit is
  // visible immediately instead of waiting for the next background poll.
  await refreshPeerSubnets(true);
  await refreshOwnerInfo(true);
  await refresh();
}

async function refresh() {
  if (refreshing) {
    refreshAgain = true;
    return;
  }
  refreshing = true;
  do {
    refreshAgain = false;
    try {
      status = await backend.status();
    } catch {
      status = null;
    }
    // Render the shell and tabs from the local status immediately. Owner/peer
    // requests go to the coordination server and can take seconds (or time
    // out on a flaky uplink); waiting for them before the first paint left the
    // whole page blank.
    renderHeader();
    renderNetworks();
    await Promise.all([refreshOwnerInfo(), refreshPeerSubnets(), refreshWireGuardExports()]);
    renderHeader();
    renderNetworks();
    renderNetworkDetail();
    renderStatus();
  } while (refreshAgain);
  refreshing = false;
}

/* ── Owner info cache ─────────────────────────────────────────── */
const ownerInfo: Record<string, NetInfoDetail> = {};
// WireGuard exports are stored by the local daemon (never the coordination
// server) and fetched here so each generated device card can reopen its
// config/QR after a page refresh.
const wgExports: Record<string, Record<string, WireGuardExport>> = {};
let lastOwnerFetch = 0;
async function refreshOwnerInfo(force = false) {
  const now = Date.now();
  if (!force && now - lastOwnerFetch < 4000) return;
  lastOwnerFetch = now;
  if (!status) return;
  const ownerNets = (status.networks ?? []).filter((n) => n.owner);
  await Promise.all(
    ownerNets.map(async (n) => {
      try {
        ownerInfo[n.networkId] = await withTimeout(backend.netinfo(n.networkId), 5000, "网络详情加载超时");
      } catch {
        delete ownerInfo[n.networkId];
      }
    }),
  );
}

/* ── Peer advertised subnets (any member) ─────────────────────── */
// Full peer snapshots are kept for the path monitor so it can show names,
// virtual IPs and direct-path details without issuing another request.
const peerDetails: Record<string, PeersResp> = {};
const peerErrors: Record<string, string> = {};
// Member online counts come from the coordination server's LastSeen window
// (the same source the Android client uses). WireGuard handshake age only
// proves recent data-plane traffic, so an idle-but-connected member would
// otherwise be miscounted as offline.
const peerSummary: Record<string, { online: number; total: number }> = {};
let lastPeerSubnetsFetch = 0;

function withTimeout<T>(p: Promise<T>, ms: number, message: string): Promise<T> {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error(message)), ms);
    p.then(
      (value) => { clearTimeout(timer); resolve(value); },
      (err) => { clearTimeout(timer); reject(err); },
    );
  });
}

async function refreshPeerSubnets(force = false) {
  const now = Date.now();
  if (!force && now - lastPeerSubnetsFetch < 10000) return;
  lastPeerSubnetsFetch = now;
  if (!status) return;
  await Promise.all(
    (status.networks ?? []).map(async (n) => {
      try {
        const r = await withTimeout(backend.peers(n.networkId), 5000, "成员信息加载超时");
        peerDetails[n.networkId] = r;
        delete peerErrors[n.networkId];
        const list = r.peers ?? [];
        const selfOnline = r.self ? !!r.self.online : false;
        peerSummary[n.networkId] = {
          online: list.filter((p) => p.online).length + (selfOnline ? 1 : 0),
          total: list.length + 1,
        };
      } catch (e) {
        // A slow/unreachable coordination request is often transient. Keep the
        // last successful snapshot instead of flashing a fatal error and
        // blanking the member cards.
        if (!peerDetails[n.networkId]) {
          delete peerSummary[n.networkId];
          peerErrors[n.networkId] = "该网络已失效或加载失败";
        }
      }
    }),
  );
}

function initWailsWindowDrag() {
  const wails = (window as any)._wails;
  if (typeof wails?.invoke !== "function") return;
  const header = document.querySelector<HTMLElement>("header");
  if (!header) return;
  header.addEventListener("mousedown", (e) => {
    if (e.button !== 0 || e.detail !== 1) return;
    const target = e.target as HTMLElement;
    if (target.closest("button, a, input, select, label, .network-tabs, .header-dropdown, .switch")) return;
    e.preventDefault();
    wails.invoke("wails:drag");
  }, true);
}

/* ── Header ───────────────────────────────────────────────────── */
/* ── Header ───────────────────────────────────────────────────── */
function isWailsWindows(): boolean {
  return backend.hasDaemonControl && /Windows/i.test(`${navigator.userAgent} ${navigator.platform}`);
}

function renderHeader() {
  const dot = $("#daemon-state") as HTMLElement;
  const ver = $("#app-version") as HTMLElement | null;
  if (ver) {
    const v = status?.version;
    if (v) {
      ver.textContent = "v" + v;
      ver.hidden = false;
    } else {
      ver.hidden = true;
    }
  }
  if (!dot) return;
  if (backend.hasDaemonControl) {
    const btn = $("#header-svc-btn") as HTMLButtonElement;
    if (isWailsWindows()) {
      const err = desktopDaemon.error;
      dot.className = status ? "live-dot ok" : err ? "live-dot err" : "live-dot";
      dot.textContent = status ? "运行中" : err ? "服务启动失败" : "正在连接服务…";
      if (btn) {
        btn.hidden = !err;
        btn.disabled = false;
        if (err) {
          btn.textContent = "重试";
          btn.title = err;
          btn.onclick = () => void retryDesktopDaemon();
        }
      }
      return;
    }
    if (status) {
      dot.className = "live-dot ok";
      dot.textContent = "运行中";
    } else {
      dot.className = "live-dot err";
      dot.textContent = "未运行";
    }
    if (btn) {
      btn.hidden = !!status;
      btn.disabled = false;
      if (!status) {
        btn.textContent = "启动";
        btn.title = "启动系统后台守护进程（可能弹出管理员密码框）";
        btn.onclick = () => void ensureDaemon();
      }
    }
  } else {
    // Web mode: always show running
    dot.className = "live-dot ok";
    dot.textContent = "snetd";
  }
}

/* ── Network cards ────────────────────────────────────────────── */
function netCard(n: { networkId: string; name?: string; ip?: string; subnet?: string; interface?: string; active?: boolean; owner?: boolean; serverState?: string; error?: string; state?: string; peerStats?: Record<string, { RxBytes?: number; TxBytes?: number; LastHandshakeSec?: number }>; visibility?: string; description?: string; tags?: string[]; role?: string }): string {
  const gone = n.serverState === "gone";
  const linked = !!n.interface;
  const hasErr = !!n.error && !gone;
  const state = gone ? "已删除" : hasErr ? "异常" : linked ? "已链接" : n.active ? "未就绪" : "未链接";
  const cls = gone ? "gone" : hasErr ? "err" : linked ? "ok" : n.active ? "warn" : "off";
  const peers = n.peerStats ?? {};
  const total = Object.keys(peers).length;
  const detail = n.owner ? ownerInfo[n.networkId] : undefined;
  const summary = peerSummary[n.networkId];
  const memberTotal = detail ? detail.nodes?.length ?? total + 1 : summary?.total ?? total + 1;
  const memberOnline = detail
    ? (detail.nodes ?? []).filter((nd) => nd.online).length
    : summary?.online ?? onlineCount(n) + (linked ? 1 : 0);
  const pendingCount = detail?.pendingCount ?? detail?.pending?.length ?? 0;
  const sharePill = (n.visibility === "shareable") ? `<span class="pill share" title="可被社区发现">shareable</span>` : "";
  const memberLine = gone
    ? `<span class="muted">已失效</span>`
    : memberTotal <= 1
      ? `<span class="muted">暂无其他成员</span>`
      : `<button type="button" data-act="members" class="meta-link">成员 <b>${memberOnline}/${memberTotal}</b> 在线</button>`;
  const tx = Object.values(peers).reduce((a, p) => a + (p.TxBytes ?? 0), 0);
  const rx = Object.values(peers).reduce((a, p) => a + (p.RxBytes ?? 0), 0);
  const pendingPill = !gone && pendingCount > 0 ? `<button class="pill pending" data-act="show-pending" title="点击查看待批准请求">待批准 (${pendingCount})</button>` : "";
  const err = gone ? `<p class="msg">${esc(n.error || "该网络已在服务端被删除")}</p>` : n.error ? `<p class="msg">${esc(n.error)}</p>` : "";
  return `
  <div class="net" data-nid="${esc(n.networkId)}" data-owner="${n.owner ? "1" : "0"}" ${gone ? 'data-gone="1"' : ""}>
    <div class="net-row">
      <div class="net-main">
        <div class="net-name">${esc(n.name || n.networkId)} ${n.owner ? `<span class="pill owner">owner</span>` : ""} ${sharePill} ${pendingPill}</div>
        <div class="net-meta">
          <span>IP <code>${esc(n.ip ?? "-")}</code></span>
          <span>网段 <code>${esc(n.subnet ?? "-")}</code></span>
        </div>
        <div class="net-stats">${memberLine}<span>收 <b>${fmtBytes(rx)}</b></span><span>发 <b>${fmtBytes(tx)}</b></span></div>
      </div>
      <div class="net-side">
        <span class="pill ${cls}">${state}</span>
        <label class="switch" title="${gone ? "该网络已删除" : linked ? "断开该网络" : "连接该网络"}">
          <input type="checkbox" data-act="toggle" ${linked ? "checked" : ""} ${gone ? "disabled" : ""} />
          <span class="slider"></span>
        </label>
        <div class="net-menu-wrap">
          <button class="net-more" data-act="more" title="更多操作" aria-haspopup="menu">
            <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor"><circle cx="5" cy="12" r="1.8"/><circle cx="12" cy="12" r="1.8"/><circle cx="19" cy="12" r="1.8"/></svg>
          </button>
        </div>
      </div>
    </div>
    ${err}
  </div>`;
}

/* ── Network card "more" menu ─────────────────────────────────── */
function buildNetMenu(nid: string, isOwner: boolean, gone: boolean): HTMLElement {
  const card = document.querySelector<HTMLElement>(`.net[data-nid="${CSS.escape(nid)}"]`);
  const wrap = card?.querySelector('.net-menu-wrap');
  if (!wrap) return document.createElement('div');
  wrap.querySelector('.net-menu')?.remove();
  const menu = document.createElement('div');
  menu.className = 'net-menu';
  menu.setAttribute('role', 'menu');
  const iconMembers = '<svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M17 21v-2a4 4 0 0 0-4-4H5a4 4 0 0 0-4 4v2"/><circle cx="9" cy="7" r="4"/><path d="M23 21v-2a4 4 0 0 0-3-3.87"/><path d="M16 3.13a4 4 0 0 1 0 7.75"/></svg>';
  const iconInvite = '<svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M16 21v-2a4 4 0 0 0-4-4H5a4 4 0 0 0-4 4v2"/><circle cx="8.5" cy="7" r="4"/><line x1="20" y1="8" x2="20" y2="14"/><line x1="23" y1="11" x2="17" y2="11"/></svg>';
  const iconSettings = '<svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1 0 2.83 2 2 0 0 1-2.83 0l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-2 2 2 2 0 0 1-2-2v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83 0 2 2 0 0 1 0-2.83l.06-.06A1.65 1.65 0 0 0 4.68 15a1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1-2-2 2 2 0 0 1 2-2h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 0-2.83 2 2 0 0 1 2.83 0l.06.06A1.65 1.65 0 0 0 9 4.68a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 2-2 2 2 0 0 1 2 2v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 0 2 2 0 0 1 0 2.83l-.06.06A1.65 1.65 0 0 0 19.4 9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 2 2 2 2 0 0 1-2 2h-.09a1.65 1.65 0 0 0-1.51 1z"/></svg>';
  const iconSubnets = '<svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="2" y="2" width="20" height="8" rx="2" ry="2"/><rect x="2" y="14" width="20" height="8" rx="2" ry="2"/><line x1="6" y1="6" x2="6.01" y2="6"/><line x1="6" y1="18" x2="6.01" y2="18"/></svg>';
  const iconInfo = '<svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="10"/><line x1="12" y1="16" x2="12" y2="12"/><line x1="12" y1="8" x2="12.01" y2="8"/></svg>';
  const iconTrash = '<svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="3 6 5 6 21 6"/><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/></svg>';
  const iconExit = '<svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4"/><polyline points="16 17 21 12 16 7"/><line x1="21" y1="12" x2="9" y2="12"/></svg>';
  if (gone) {
    // The network is gone on the server; only allow the user to drop the
    // local record. Server-side ops are blocked by the action guard.
    menu.innerHTML = `
      <button type="button" data-act="delete" data-nid="${esc(nid)}" class="danger">${iconTrash} 删除本地记录</button>`;
  } else if (isOwner) {
    menu.innerHTML = `
      <button type="button" data-act="info" data-nid="${esc(nid)}">${iconInfo} 详情</button>
      <button type="button" data-act="members" data-nid="${esc(nid)}">${iconMembers} 查看成员</button>
      <button type="button" data-act="invite" data-nid="${esc(nid)}">${iconInvite} 邀请</button>
      <button type="button" data-act="settings" data-nid="${esc(nid)}">${iconSettings} 设置</button>
      <button type="button" data-act="subnets" data-nid="${esc(nid)}">${iconSubnets} 子网路由</button>
      <hr/>
      <button type="button" data-act="delete" data-nid="${esc(nid)}" class="danger">${iconTrash} 删除网络</button>`;
  } else {
    menu.innerHTML = `
      <button type="button" data-act="subnets" data-nid="${esc(nid)}">${iconSubnets} 子网路由</button>
      <button type="button" data-act="members" data-nid="${esc(nid)}">${iconMembers} 查看成员</button>
      <hr/>
      <button type="button" data-act="remove" data-nid="${esc(nid)}" class="danger">${iconExit} 退出网络</button>`;
  }
  wrap.appendChild(menu);
  const mr = menu.getBoundingClientRect();
  const btnEl = wrap.querySelector('.net-more');
  const btnTop = btnEl ? btnEl.getBoundingClientRect().top : 0;
  if (mr.bottom > window.innerHeight - 8 && mr.height < btnTop - 8) {
    menu.classList.add('up');
  }
  return menu;
}

function closeNetMenus() {
  document.querySelectorAll('.net-menu').forEach(m => m.remove());
  document.querySelectorAll('.net-more.open').forEach(b => b.classList.remove('open'));
}

function toggleNetMenu(btn: HTMLButtonElement, nid: string, gone: boolean) {
  const wrap = btn.closest('.net-menu-wrap');
  const isOpen = !!wrap?.querySelector('.net-menu');
  closeNetMenus();
  if (isOpen) return;
  btn.classList.add('open');
  const card = btn.closest<HTMLElement>('.net');
  const isOwner = card?.dataset.owner === "1";
  buildNetMenu(nid, isOwner, gone);
}

function sortedNetworks(): NetInfo[] {
  return [...(status?.networks ?? [])].sort((a, b) => {
    const ta = a.joinedAt || "";
    const tb = b.joinedAt || "";
    if (ta && tb) return ta.localeCompare(tb);
    if (ta) return -1;
    if (tb) return 1;
    return (a.networkId || "").localeCompare(b.networkId || "");
  });
}

function renderNetworkTabs(nets: NetInfo[]) {
  const tabs = $("#network-tabs");
  if (!tabs) return;
  const pending = status?.pendingJoins ?? [];
  const html = nets.filter((n) => n.serverState !== "gone").map((n) => {
    const active = n.networkId === detailNid ? " active" : "";
    const dot = n.interface ? "ok" : "off";
    return `<button type="button" class="network-tab${active}" data-nid="${esc(n.networkId)}" title="${esc(n.name || n.networkId)}"><span class="network-dot ${dot}"></span>${esc(n.name || n.networkId)}</button>`;
  }).join("") + pending.map((p) => {
    const active = p.pendingId === detailPendingId ? " active" : "";
    return `<button type="button" class="network-tab pending${active}" data-pid="${esc(p.pendingId)}" title="等待批准 · ${esc(p.networkId)}"><span class="network-dot warn"></span>${esc(p.networkId)}</button>`;
  }).join("") + '<button type="button" class="network-tab-add" id="btn-add-network" title="创建或加入网络">+</button>';
  if (html !== lastTabsHTML) {
    tabs.innerHTML = html;
    lastTabsHTML = html;
  }
}

function openNetworkChooser() {
  const hasOwner = (status?.networks ?? []).some((n) => n.owner && n.serverState !== "gone");
  openModal({
    title: "添加网络",
    body: `<div class="network-choice">
      <button type="button" class="network-choice-card${hasOwner ? " disabled" : ""}" id="choice-create" ${hasOwner ? 'aria-disabled="true"' : ""}>
        <span class="choice-icon">+</span>
        <b>创建新网络</b>
        <small>${hasOwner ? "你已创建网络，不支持再创建" : "创建一个新的虚拟网络并邀请设备加入"}</small>
      </button>
      <button type="button" class="network-choice-card" id="choice-join">
        <span class="choice-icon">→</span>
        <b>加入现有网络</b>
        <small>粘贴邀请链接加入已有网络</small>
      </button>
    </div>`,
    footer: `<button data-close class="btn ghost">取消</button>`,
    onBody: (body) => {
      body.querySelector("#choice-create")?.addEventListener("click", () => {
        if (hasOwner) { toast("你已创建网络，不支持再创建", "warn"); return; }
        closeModal();
        openCreateModal();
      });
      body.querySelector("#choice-join")?.addEventListener("click", () => {
        closeModal();
        openJoinModal();
      });
    },
  });
}

function renderNetworks() {
  const nets = sortedNetworks().filter((n) => n.serverState !== "gone");
  const pending = status?.pendingJoins ?? [];
  const hasContent = !!status && (nets.length > 0 || pending.length > 0);
  const empty = $("#empty-state");
  const main = $("#app-main");
  if (empty) empty.hidden = hasContent;
  if (main) main.hidden = !hasContent;
  document.body.classList.toggle("empty-mode", !hasContent);
  const networkTabs = $("#network-tabs");
  if (networkTabs) networkTabs.hidden = !hasContent;

  if (!hasContent) {
    detailNid = null;
    detailPendingId = null;
    renderNetworkTabs(nets);
    return;
  }
  if (detailPendingId && !pending.some((p) => p.pendingId === detailPendingId)) {
    detailPendingId = null;
  }
  if (detailNid && !nets.some((n) => n.networkId === detailNid && n.serverState !== "gone")) {
    detailNid = null;
  }
  if (!detailNid && !detailPendingId) {
    detailNid = nets.find((n) => n.serverState !== "gone")?.networkId ?? null;
    if (!detailNid && pending.length) detailPendingId = pending[0].pendingId;
  }
  renderNetworkTabs(nets);
}

/* ── Network card events (delegation) ─────────────────────────── */
function bindNetListEvents() {
  const netList = $("#net-list");
  if (!netList) return;

  netList.addEventListener("click", async (e) => {
    const target = e.target as HTMLElement;
    // Handle "more" button
    const moreBtn = target.closest<HTMLButtonElement>(".net-more");
    if (moreBtn) {
      e.stopPropagation();
      const card = moreBtn.closest<HTMLElement>(".net");
      if (card) toggleNetMenu(moreBtn, card.dataset.nid!, card.dataset.gone === "1");
      return;
    }
    // Handle menu item clicks
    const menuBtn = target.closest<HTMLButtonElement>(".net-menu button[data-act]");
    if (menuBtn) {
      e.stopPropagation();
      const nid = menuBtn.dataset.nid || menuBtn.closest<HTMLElement>(".net")?.dataset.nid;
      const pid = menuBtn.closest<HTMLElement>(".net")?.dataset.pid;
      closeNetMenus();
      if (!nid) return;
      const act = menuBtn.dataset.act!;
      if (act === "cancel-pending") {
        if (!(await confirmDialog("取消加入请求", "取消等待批准？", true))) return;
        try {
          await backend.cancelPending(pid!);
          toast("已取消加入请求");
        } catch (err) {
          toast(String(err), "err");
        }
        await refresh();
        return;
      }
      if (act === "show-pending") {
        await showPending(nid);
        return;
      }
      await onAction(act, nid, menuBtn);
      return;
    }
    // Handle card actions (pending cancel, members shortcut, etc.).
    const actEl = target.closest<HTMLElement>("[data-act]");
    if (actEl) {
      const card = actEl.closest<HTMLElement>(".net");
      if (!card) return;
      const act = actEl.dataset.act!;
      if (act === "cancel-pending") {
        if (!(await confirmDialog("取消加入请求", "取消等待批准？", true))) return;
        try {
          await backend.cancelPending(card.dataset.pid!);
          toast("已取消加入请求");
        } catch (err) {
          toast(String(err), "err");
        }
        await refresh();
        return;
      }
      if (act === "show-pending") {
        await showPending(card.dataset.nid!);
        return;
      }
      if (act === "members") {
        await onAction("members", card.dataset.nid!, actEl as HTMLButtonElement);
        return;
      }
      await onAction(act, card.dataset.nid!, actEl as HTMLButtonElement);
      return;
    }

    // Clicking the card background opens the in-panel network detail view.
    const card = target.closest<HTMLElement>(".net");
    if (!card || card.classList.contains("pending") || card.dataset.gone === "1") return;
    if (target.closest("button, a, input, select, label, .switch, .net-menu, .net-more")) return;
    await openNetworkDetail(card.dataset.nid!);
  });

  // Close menus on outside click
  document.addEventListener("click", (e) => {
    if (!(e.target as HTMLElement).closest('.net-menu') && !(e.target as HTMLElement).closest('.net-more')) {
      closeNetMenus();
    }
  });
  document.addEventListener("keydown", (e) => {
    if (e.key === 'Escape') closeNetMenus();
  });

  netList.addEventListener("change", async (e) => {
    const input = e.target as HTMLInputElement;
    if (input.dataset.act !== "toggle") return;
    const card = input.closest<HTMLElement>(".net");
    if (!card) return;
    await onToggle(card, input.checked);
  });
}

async function onToggle(card: HTMLElement, checked: boolean) {
  const nid = card.dataset.nid!;
  const input = card.querySelector<HTMLInputElement>('input[data-act="toggle"]')!;
  if (card.dataset.gone === "1") {
    input.checked = false;
    toast("该网络已从服务端删除");
    return;
  }
  input.disabled = true;
  card.classList.add("busy");
  // Flip the switch optimistically so the UI responds instantly; the real
  // state is reconciled by refresh() once the background op finishes (or
  // reverts below on failure).
  input.checked = checked;
  try {
    if (checked) await backend.rejoin(nid);
    else await backend.leave(nid);
    toast(checked ? `已连接 ${nid}` : `已断开 ${nid}`);
  } catch (e) {
    input.checked = !checked;
    toast(String(e), "err");
  } finally {
    input.disabled = false;
    card.classList.remove("busy");
    await refresh();
  }
}

let actionBusy = false;
async function onAction(act: string, nid: string, btn: HTMLButtonElement) {
  if (actionBusy) return;
  actionBusy = true;
  const origText = btn.textContent;
  btn.disabled = true;
  const setPending = (t: string) => { btn.innerHTML = `${SPIN} ${t}`; };
  try {
    // Block all ops except "delete" / "remove" for a network that has been
    // deleted from the server.
    if (act !== "delete" && act !== "remove") {
      const net = (status?.networks ?? []).find((n) => n.networkId === nid);
      if (net?.serverState === "gone") {
        toast("该网络已从服务端删除，仅可删除本地记录");
        return;
      }
    }
    switch (act) {
      case "info": {
        setPending("查询中…");
        await openNetworkDetailModal(nid);
        break;
      }
      case "members": { setPending("查询中…"); await showMembers(nid); break; }
      case "settings": { await openNetworkSettings(nid); break; }
      case "subnets": { await openSubnetRouteModal(nid); break; }
      case "invite": { setPending("生成中…"); await openInviteModal(nid); break; }
      case "delete": {
        if (!(await confirmDialog("删除网络", "删除将断开所有成员并释放网段，不可恢复。确定？", true))) return;
        setPending("删除中…");
        const r = await ctlOrClean(nid, "删除网络", () => backend.deleteNet(nid));
        if (r === null) break;
        toast("网络已删除");
        break;
      }
      case "remove": {
        if (!(await confirmDialog("退出网络", "退出后需重新扫码加入。继续？", true))) return;
        setPending("退出中…");
        await backend.remove(nid);
        toast("已退出网络");
        break;
      }
    }
  } catch (e) {
    toast(String(e), "err");
  } finally {
    actionBusy = false;
    btn.disabled = false;
    btn.textContent = origText;
    await refresh();
  }
}

async function ctlOrClean<T>(nid: string, label: string, fn: () => Promise<T>): Promise<T | null> {
  try {
    return await fn();
  } catch (e: any) {
    if (String(e).includes("该网络在服务端已不存在")) {
      const ok = await confirmDialog(`${label}失败`, "该网络在服务端已不存在，是否清理本地配置？", true);
      if (ok) {
        await backend.remove(nid);
        toast("本地网络配置已清理");
        await refresh();
      }
      return null;
    }
    throw e;
  }
}

/* ── Network settings modal ───────────────────────────────────── */
async function openNetworkSettings(nid: string) {
  let detail: NetInfoDetail | undefined = ownerInfo[nid];
  if (!detail) {
    try {
      detail = await backend.netinfo(nid);
      ownerInfo[nid] = detail;
    } catch (e) {
      toast(`获取网络信息失败: ${e}`, "err");
      return;
    }
  }
  const curName = detail.name || "";
  const curSubnet = detail.subnet || "";
  openModal({
    title: `网络设置 · ${nid}`,
    body: `<div class="network-form">
      <div class="form-stack">
        <div class="form-field"><label for="s-name">网络名称</label><input id="s-name" class="form-control" type="text" value="${esc(curName)}" placeholder="留空保持不变" maxlength="64" /></div>
        <div class="form-field"><label>虚拟网段</label>${subnetWidgetHTML({ id: "s-subnet", value: curSubnet, placeholder: "留空保持不变", emptyHint: "留空保持不变" })}</div>
      </div>
      <div class="settings-row form-switch-row">
        <div class="settings-row-main"><div class="settings-row-title">新成员加入需创建者批准</div><div class="settings-row-desc">开启后，成员加入请求会出现在待审批列表。</div></div>
        <label class="switch"><input id="s-approval" type="checkbox" ${detail.approvalRequired ? "checked" : ""} /><span class="slider"></span></label>
      </div>
      <p class="msg" id="s-warn" hidden>修改网段会重新分配所有成员 IP，已加入的 SNET 客户端会自动重连；但手机等外部 WireGuard 设备需手动重新导入新配置。</p>
      <p class="msg" id="s-result"></p>
    </div>`,
    footer: `<button data-close class="btn ghost">取消</button><button id="m-submit" class="btn">保存</button>`,
    onBody: (body) => {
      const subnetWidget = subnetWidgetInit(body.querySelector<HTMLElement>(".subnet-wrap")!);
      const warn = body.querySelector<HTMLElement>("#s-warn")!;
      const subnetInput = body.querySelector<HTMLInputElement>("#s-subnet")!;
      subnetInput.addEventListener("input", () => {
        warn.hidden = subnetWidget.get() === "" || subnetWidget.get() === curSubnet;
      });
      body.querySelector("#m-submit")!.addEventListener("click", async () => {
        const name = (body.querySelector("#s-name") as HTMLInputElement).value.trim();
        const chk = subnetWidget.check();
        if (!chk.ok) {
          body.querySelector<HTMLElement>("#s-result")!.className = "msg";
          body.querySelector<HTMLElement>("#s-result")!.textContent = `网段无效：${chk.error}`;
          return;
        }
        const subnet = chk.value;
        const approval = (body.querySelector("#s-approval") as HTMLInputElement).checked;
        const result = body.querySelector<HTMLElement>("#s-result")!;
        const submit = body.querySelector<HTMLButtonElement>("#m-submit")!;
        const origText = submit.textContent;
        submit.disabled = true;
        result.className = "msg";
        result.textContent = "保存中…";
        try {
          const r = await ctlOrClean(nid, "保存设置", async () => {
            await backend.updateSettings({
              nid,
              name,
              subnet: subnet !== curSubnet ? subnet : "",
              approvalRequired: approval !== !!detail!.approvalRequired ? approval : null,
            });
            return true;
          });
          if (r === null) return;
          result.className = "msg ok";
          result.textContent = "已保存";
          await refreshOwnerInfo(true);
          toast("网络设置已保存");
          closeModal();
        } catch (e) {
          result.className = "msg";
          result.textContent = `保存失败: ${e}`;
          submit.disabled = false;
          submit.textContent = origText;
        }
      });
    },
  });
}

/* ── Subnet route modal ───────────────────────────────────────── */
async function openSubnetRouteModal(nid: string) {
  const st = await backend.status();
  const net = (st?.networks ?? []).find((n) => n.networkId === nid);
  const canManage = !!net && canManageNetwork(net);

  // Owner view: the delegated relay routes of the network plus the nodes
  // that can be picked as the gateway for a delegated subnet.
  let routes: RelayRoute[] = [];
  let nodes: NetNode[] = [];
  if (canManage) {
    try {
      const info = await backend.netinfo(nid);
      ownerInfo[nid] = info;
      routes = info.routes ?? [];
      nodes = (info.nodes ?? []) as unknown as NetNode[];
    } catch {
      /* fall back to the peer snapshot below */
    }
  }
  if (!nodes.length) {
    const r = peerDetails[nid];
    nodes = r ? [...(r.peers ?? []), ...(r.self ? [r.self] : [])] : [];
  }
  const selfID = net?.networkId ? (peerDetails[nid]?.self?.id ?? "") : "";
  // Every node of the network can be the gateway, including this one: the
  // owner often is the machine that reaches the delegated LAN. A route
  // delegated to the owner is honoured by the gateway itself (it forwards
  // the traffic towards the LAN) and by the other members (they send the
  // traffic through the owner's tunnel).
  const targets = nodes.filter((n) => n.id);

  const renderRows = (list: RelayRoute[]) => {
    if (!list.length) {
      return `<tr><td colspan="4" class="route-empty">暂无委派路由</td></tr>`;
    }
    return list
      .map((rt) => {
        const node = nodes.find((n) => n.id === rt.targetNodeId);
        const target = node ? deviceLabel(node) : `<span class="mono">${esc(rt.targetNodeId.slice(0, 8))}</span>`;
        const failed = !!node?.relayError;
        const active = rt.status === "active" && !failed;
        const badgeClass = failed ? "err" : active ? "ok" : "pending";
        const badgeText = failed ? "配置失败" : active ? "已生效" : "待确认";
        const why = failed
          ? `<div class="route-row-error">${esc(node?.relayError ?? "")}</div>`
          : "";
        return `<tr>
          <td class="route-target">${target}</td>
          <td><code class="route-cidr">${esc(rt.cidr)}</code>${why}</td>
          <td><span class="route-badge ${badgeClass}">${badgeText}</span></td>
          <td class="route-actions"><button type="button" class="route-del" data-del-route="${esc(rt.targetNodeId)}|${esc(rt.cidr)}" title="删除该路由" aria-label="删除该路由">🗑️</button></td>
        </tr>`;
      })
      .join("");
  };

  const ownerSection = canManage
    ? `<div class="route-config">
        <div class="route-form">
          <div class="route-field route-field-node">
            <label for="sr-target">目标节点</label>
            <select id="sr-target" class="form-control" ${targets.length ? "" : "disabled"}>
              ${targets.length
                ? targets
                    .map((n) => {
                      const relay = n.allowRelay ? "（已开启中继）" : n.relayError ? "（网关不可用）" : "（未开启中继）";
                      const who = n.id === selfID ? "本机" : "";
                      return `<option value="${esc(n.id)}">${esc(who ? who + " · " : "")}${esc(deviceLabelText(n))} · ${esc(n.ip || "-")}${esc(relay)}</option>`;
                    })
                    .join("")
                : `<option value="">（无可选节点）</option>`}
            </select>
          </div>
          <div class="route-field route-field-cidr">
            <label for="sr-cidr">委派网段 (CIDR)</label>
            <input type="text" id="sr-cidr" class="form-control" placeholder="例如: 192.168.50.0/24" autocomplete="off" spellcheck="false" />
          </div>
          <button id="sr-route-add" class="btn route-add-btn" type="button" ${targets.length ? "" : "disabled"}><span class="route-add-plus">+</span> 添加委派</button>
        </div>
        <p class="msg" id="sr-route-result"></p>
        <div class="route-list-head">已委派路由</div>
        <div class="route-table-wrap">
          <table class="route-table">
            <thead><tr><th>目标节点</th><th>委派网段</th><th>状态</th><th>操作</th></tr></thead>
            <tbody id="sr-route-rows">${renderRows(routes)}</tbody>
          </table>
        </div>
      </div>`
    : `<p class="hint">仅创建者可以委派子网路由。</p>`;

  openModal({
    title: `子网路由 · ${nid}`,
    wide: true,
    body: ownerSection,
    footer: `<button data-close class="btn ghost">关闭</button>`,
    onBody: (body) => {
      const rowsEl = body.querySelector<HTMLElement>("#sr-route-rows");
      const targetEl = body.querySelector<HTMLSelectElement>("#sr-target");
      const cidrEl = body.querySelector<HTMLInputElement>("#sr-cidr");
      const addBtn = body.querySelector<HTMLButtonElement>("#sr-route-add");
      const routeResult = body.querySelector<HTMLElement>("#sr-route-result");
      let draft: RelayRoute[] = routes.map((r) => ({ ...r }));

      const rerender = () => {
        if (rowsEl) rowsEl.innerHTML = renderRows(draft);
      };

      const syncFromServer = async () => {
        const info = await backend.netinfo(nid);
        ownerInfo[nid] = info;
        routes = info.routes ?? [];
        nodes = (info.nodes ?? []) as unknown as NetNode[];
        draft = routes.map((r) => ({ ...r }));
        rerender();
      };

      const persist = async (list: RelayRoute[], okText: string): Promise<boolean> => {
        if (!addBtn) return false;
        addBtn.disabled = true;
        if (routeResult) { routeResult.className = "msg"; routeResult.textContent = "保存中…"; }
        try {
          const r = await ctlOrClean(nid, "委派路由", async () => {
            await backend.setRoutes({
              nid,
              routes: list.map((x) => ({ targetNodeId: x.targetNodeId, cidr: x.cidr })),
            });
            return true;
          });
          if (r === null) return false;
          await syncFromServer();
          if (routeResult) { routeResult.className = "msg ok"; routeResult.textContent = okText; }
          toast("委派路由已更新");
          return true;
        } catch (e) {
          if (routeResult) { routeResult.className = "msg"; routeResult.textContent = `保存失败: ${e}`; }
          return false;
        } finally {
          addBtn.disabled = targets.length === 0;
        }
      };

      if (rowsEl && targetEl && cidrEl && addBtn) {
        const addRoute = async () => {
          const target = targetEl.value;
          if (!target) { if (routeResult) routeResult.textContent = "请选择目标节点"; return; }
          const raw = cidrEl.value.trim();
          if (!raw) { if (routeResult) routeResult.textContent = "请输入委派网段"; return; }
          const chk = subnetCheck(raw, 24);
          if (!chk.ok) {
            if (routeResult) { routeResult.className = "msg"; routeResult.textContent = `无效: ${chk.error}`; }
            return;
          }
          const cidr = chk.value;
          if (draft.some((r) => r.cidr === cidr)) {
            if (routeResult) { routeResult.className = "msg"; routeResult.textContent = "该网段已被其他节点委派，不可重复添加"; }
            return;
          }
          const ok = await persist([...draft, { targetNodeId: target, cidr, status: "pending" }], "委派已添加，等待目标节点确认");
          if (ok) cidrEl.value = "";
        };

        addBtn.addEventListener("click", () => void addRoute());
        cidrEl.addEventListener("keydown", (e) => {
          if (e.key === "Enter") { e.preventDefault(); void addRoute(); }
        });
        rowsEl.addEventListener("click", async (e) => {
          const btn = (e.target as HTMLElement).closest<HTMLButtonElement>("[data-del-route]");
          if (!btn) return;
          const [nodeId, cidr] = (btn.dataset.delRoute ?? "").split("|");
          const next = draft.filter((r) => !(r.targetNodeId === nodeId && r.cidr === cidr));
          if (next.length === draft.length) return;
          await persist(next, "委派路由已删除");
        });
      }
    },
  });
}

/* ── Invite modal ─────────────────────────────────────────────── */
async function openInviteModal(nid: string, known?: { link: string; name?: string }) {
  let info: { link: string; name?: string } | null = known ?? null;
  if (!info) {
    // Never fail silently: a missing invite link has to tell the user why.
    try {
      info = await inviteInfo(nid);
    } catch (e) {
      const msg = String(e).replace(/^Error:\s*/, "") || "未知错误";
      toast(/404/.test(msg) ? `获取邀请链接失败: ${msg}（客户端版本过旧，请更新镜像）` : `获取邀请链接失败: ${msg}`, "err");
      return;
    }
    if (!info) return;
  }
  const name = info.name || "";
  let link = info.link;
  openModal({
    title: name ? `邀请加入 · ${name}` : "邀请加入",
    wide: true,
    body: `
      <div class="invite-layout">
        <div class="invite-qr-side">
          <div class="qr invite-qr" id="qr"></div>
          <p class="hint">扫描二维码加入</p>
        </div>
        <div class="invite-info">
          <p class="hint invite-lede">把邀请链接发给对方，对方在「加入网络」里粘贴即可加入。</p>
          <div class="invite-link-box">
            <code class="invite-link" id="m-link">${esc(link)}</code>
          </div>
          <button type="button" class="btn invite-copy" id="m-copy">复制邀请链接</button>
          <p class="hint invite-note">链接可随时重置：重置后旧链接立即失效，已加入的成员不受影响。</p>
        </div>
      </div>`,
    footer: `<button data-close class="btn ghost">关闭</button><button type="button" class="btn danger ghost" id="m-reset">重置邀请链接</button>`,
    onBody: (body) => {
      renderQR(body.querySelector<HTMLElement>("#qr")!, link);
      body.querySelector("#m-copy")?.addEventListener("click", async (e) => {
        await copyText(link, e.currentTarget as HTMLButtonElement);
      });
      const linkEl = body.querySelector<HTMLElement>("#m-link");
      const resetBtn = body.querySelector<HTMLButtonElement>("#m-reset");
      let resetArmed = false;
      let resetTimer = 0;
      resetBtn?.addEventListener("click", async () => {
        if (!resetArmed) {
          resetArmed = true;
          resetBtn.textContent = "再次点击确认重置";
          resetBtn.classList.add("danger");
          resetTimer = window.setTimeout(() => {
            resetArmed = false;
            resetBtn.textContent = "重置邀请链接";
            resetBtn.classList.remove("danger");
          }, 3500);
          return;
        }
        resetArmed = false;
        window.clearTimeout(resetTimer);
        resetBtn.disabled = true;
        resetBtn.textContent = "重置中…";
        try {
          const fresh = await backend.rotateInvite(nid);
          link = fresh.link;
          if (linkEl) linkEl.textContent = link;
          const qr = body.querySelector<HTMLElement>("#qr");
          if (qr) renderQR(qr, link);
          toast("新邀请链接已生成");
          await refresh();
        } catch (e) {
          toast(`重置失败: ${e}`, "err");
        } finally {
          resetBtn.disabled = false;
          resetBtn.textContent = "重置邀请链接";
          resetBtn.classList.remove("danger");
        }
      });
    },
  });
}

/* ── Rotate the invite link ───────────────────────────────────── */
// Replaces the pairing code behind the invite link, so every link shared
// before the rotation stops working. The new link is copied right away.
async function rotateInviteLink(nid: string): Promise<{ link: string; name?: string } | null> {
  if (!(await confirmDialog("重置邀请链接", "重置后，旧的邀请链接将立即失效，确定要重置吗？", true))) return null;
  try {
    const r = await ctlOrClean(nid, "重置邀请链接", () => backend.rotateInvite(nid));
    if (r === null) return null;
    await copyText(r.link, null, true);
    toast("新链接已生成并复制");
    await refresh();
    return { link: r.link, name: r.name };
  } catch (e) {
    toast(`重置失败: ${e}`, "err");
    return null;
  }
}

/* ── Pending approval modal ───────────────────────────────────── */
async function showPending(nid: string) {
  try {
    const r = await backend.netinfo(nid);
    ownerInfo[nid] = r;
    const pending = r.pending ?? [];
    if (!pending.length) {
      openModal({ title: `待批准请求 · ${nid}`, body: `<p class="muted">暂无待批准请求</p>` });
      return;
    }
    const rows = pending.map((p) => {
      return `<tr>
        <td><code>${p.deviceId ? esc(p.deviceId) : "-"}</code></td>
        <td><code>${esc(p.publicKey.slice(0, 12))}…</code></td>
        <td><span class="pill warn">待批准</span></td>
        <td>
          <button data-pend="${esc(p.id)}" class="btn sm" style="background:var(--ok)">批准</button>
          <button data-pend="${esc(p.id)}" class="btn danger sm">拒绝</button>
        </td>
      </tr>`;
    }).join("");
    openModal({
      title: `待批准请求 · ${nid}`,
      body: `<table class="members"><thead><tr><th>设备</th><th>公钥</th><th>状态</th><th>操作</th></tr></thead><tbody>${rows}</tbody></table>`,
    });
    const modal = $("#modal-body");
    if (modal) {
      modal.addEventListener("click", async (e) => {
        const btn = (e.target as HTMLElement).closest<HTMLButtonElement>("[data-pend]");
        if (!btn) return;
        const pid = btn.dataset.pend!;
        const isDeny = btn.classList.contains("danger");
        if (isDeny) {
          if (!(await confirmDialog("拒绝加入", "确定拒绝该设备的加入请求？", true))) return;
        }
        try {
          if (isDeny) await backend.deny({ nid, pendingId: pid });
          else await backend.approve({ nid, pendingId: pid });
          toast(isDeny ? "已拒绝" : "已批准");
          await refresh();
          await showPending(nid);
        } catch (err) {
          toast(String(err), "err");
        }
      });
    }
  } catch (e) {
    toast(String(e), "err");
  }
}

/* ── Members modal ────────────────────────────────────────────── */
function roleLabel(role: string): string {
  const r = String(role || "").trim().toLowerCase();
  if (r === "owner") return "创建者";
  return "成员";
}

/* ── Members modal ────────────────────────────────────────────── */
// deviceLabel renders "设备名称 (短ID)"; falls back to whichever part is present.
function deviceLabel(nd: { deviceName?: string; deviceId?: string }): string {
  const name = nd.deviceName?.trim();
  const id = nd.deviceId || "";
  if (name && id) return `${esc(name)} <span class="muted mono">(${esc(id)})</span>`;
  if (name) return esc(name);
  if (id) return `<span class="muted mono">${esc(id)}</span>`;
  return "-";
}

function deviceLabelText(nd: { deviceName?: string; deviceId?: string }): string {
  const name = nd.deviceName?.trim();
  const id = nd.deviceId || "";
  if (name && id) return `${name} (${id})`;
  if (name) return name;
  if (id) return id;
  return "-";
}

function nodeRouteCell(routes: RelayRoute[] | undefined, nodeID: string): string {
  // Only the route delegated to this exact node may be shown on its row.
  // Never borrow another gateway's CIDR and never show a pending route as if
  // it were already usable.
  const mine = (routes ?? []).filter((r) => r.targetNodeId === nodeID && r.status === "active");
  if (!mine.length) return '<span class="muted">-</span>';
  return mine
    .map((r) => `<span class="pill ok subnet-route" title="已生效">${esc(r.cidr)}</span>`)
    .join(" ");
}

async function showMembers(nid: string) {
  const mine = (status?.networks ?? []).find((n) => n.networkId === nid);
  if (mine?.serverState === "gone") {
    toast("该网络已删除，无法查看成员");
    return;
  }
  const myIp = mine?.ip;
  if (mine?.owner) {
    const r = await backend.netinfo(nid);
    ownerInfo[nid] = r;
    const rows = (r.nodes ?? []).map((nd) => {
      const online = nd.online ? '<span class="pill ok">在线</span>' : '<span class="pill off">离线</span>';
      const isSelf = nd.ip === myIp || nd.deviceId === (status?.deviceId ?? "");
      const kick = nd.ip === myIp ? `<span class="muted">自己</span>` : `<button data-node="${esc(nd.id)}" class="btn danger ghost sm">踢出</button>`;
      const role = String(nd.role || (isSelf && mine?.owner ? "owner" : "member")).trim().toLowerCase();
      const roleCtl = isSelf || role === "owner"
        ? `<span class="pill role-${esc(role)}">${roleLabel(role)}</span>`
        : `<select data-role="${esc(nd.id)}" data-cur="${esc(role)}" class="role-select">
             <option value="member" ${role === "member" ? "selected" : ""}>成员</option>
             <option value="owner" ${role === "owner" ? "selected" : ""}>创建者（转交所有权）</option>
           </select>`;
      const routeCell = nodeRouteCell(r.routes, nd.id);
      return `<tr><td class="mono">${esc(nd.ip)}</td><td>${deviceLabel(nd)}</td><td>${online}</td><td>${roleCtl}</td><td>${routeCell}</td><td>${kick}</td></tr>`;
    }).join("");
    const pendingRows = (r.pending ?? []).map((p) => {
      return `<tr><td colspan="2"><span class="muted">设备</span> <code>${p.deviceId ? esc(p.deviceId) : "-"}</code><span class="muted"> 公钥</span> <code>${esc(p.publicKey.slice(0, 12))}…</code></td><td><span class="pill warn">待批准</span></td><td>-</td><td><button data-pend="${esc(p.id)}" class="btn sm" style="background:var(--ok)">批准</button> <button data-pend="${esc(p.id)}" class="btn danger sm">拒绝</button></td></tr>`;
    }).join("");
    const pendingSection = pendingRows
      ? `<div class="pending-block"><h4>待批准加入请求</h4><div class="tbl-wrap"><table class="members"><tbody>${pendingRows}</tbody></table></div></div>`
      : "";
    openModal({
      title: `成员 · ${nid}`,
      wide: true,
      body: `<div class="tbl-wrap"><table class="members"><thead><tr><th>IP</th><th>设备</th><th>状态</th><th>角色</th><th>子网路由</th><th></th></tr></thead><tbody>${rows}</tbody></table></div>${pendingSection}`,
      onBody: (b) => {
        b.querySelectorAll<HTMLElement>("[data-node]").forEach((k) =>
          k.addEventListener("click", async () => {
            if (!(await confirmDialog("踢出成员", "踢出该成员？该设备将立即断开。", true))) return;
            const btn = k as HTMLButtonElement;
            const orig = btn.textContent;
            btn.disabled = true;
            btn.textContent = "踢出中…";
            try {
              const r = await ctlOrClean(nid, "踢出成员", () => backend.kick({ nid, nodeId: k.dataset.node! }));
              if (r === null) return;
              toast("已踢出");
              closeModal();
            } catch (e) {
              toast(String(e), "err");
              btn.disabled = false;
              btn.textContent = orig;
            }
            await refresh();
          }),
        );
        b.querySelectorAll<HTMLSelectElement>("[data-role]").forEach((sel) =>
          sel.addEventListener("change", async () => {
            const newRole = sel.value as string;
            const msg = newRole === "owner"
              ? "转交网络所有权给该成员？原创建者将变为普通成员。"
              : `将该成员设为「${roleLabel(newRole)}」？`;
            if (!(await confirmDialog("修改角色", msg, true))) {
              sel.value = sel.dataset.cur!;
              return;
            }
            const origVal = sel.value;
            sel.disabled = true;
            try {
              const r = await ctlOrClean(nid, "修改角色", () =>
                backend.setRole!({ nid, nodeId: sel.dataset.role!, role: sel.value }));
              if (r === null) { sel.disabled = false; sel.value = sel.dataset.cur!; return; }
              sel.dataset.cur = origVal;
              toast("角色已更新");
            } catch (e) {
              toast(String(e), "err");
              sel.value = sel.dataset.cur!;
            }
            sel.disabled = false;
            await refresh();
          }),
        );
        b.querySelectorAll<HTMLElement>("[data-pend]").forEach((k) => {
          const deny = k.classList.contains("danger");
          k.addEventListener("click", async () => {
            const pid = k.dataset.pend!;
            if (!(await confirmDialog(deny ? "拒绝加入请求" : "批准加入请求", deny ? "拒绝后该设备无法加入。继续？" : "批准后该设备立即加入网络。继续？", deny))) return;
            const btn = k as HTMLButtonElement;
            const orig = btn.textContent;
            btn.disabled = true;
            btn.textContent = deny ? "拒绝中…" : "批准中…";
            try {
              if (deny) await backend.deny({ nid, pendingId: pid });
              else await backend.approve({ nid, pendingId: pid });
              toast(deny ? "已拒绝" : "已批准");
              closeModal();
            } catch (e) {
              toast(String(e), "err");
              btn.disabled = false;
              btn.textContent = orig;
            }
            await refresh();
          });
        });
      },
    });
    return;
  }
  // Non-owner: read-only peers view
  const [r, info] = await Promise.all([backend.peers(nid), backend.netinfo(nid)]);
  const all = r.self ? [r.self, ...(r.peers ?? [])] : (r.peers ?? []);
  const rows = all.map((nd) => {
    const isSelf = nd.ip === myIp;
    const online = nd.online ? '<span class="pill ok">在线</span>' : '<span class="pill off">离线</span>';
    const routeCell = nodeRouteCell(info.routes ?? r.routes, nd.id);
    return `<tr><td class="mono">${esc(nd.ip)}</td><td>${deviceLabel(nd)}</td><td>${online}</td><td>${routeCell}</td><td>${isSelf ? `<span class="muted">自己</span>` : ""}</td></tr>`;
  }).join("");
  openModal({
    title: `成员 · ${nid}`,
    wide: true,
    body: `<p class="hint">成员列表（只读，本机非创建者）</p><div class="tbl-wrap"><table class="members"><thead><tr><th>IP</th><th>设备</th><th>状态</th><th>子网路由</th><th></th></tr></thead><tbody>${rows}</tbody></table></div>`,
  });
}

/* ── Create modal ─────────────────────────────────────────────── */
function openCreateModal() {
  const s = loadSettings();
  const hasOwner = (status?.networks ?? []).some((n) => n.owner && n.serverState !== "gone");
  const srv = currentServer();
  const hint = hasOwner
    ? `<div class="form-warning">你已创建网络，不支持再创建。请先删除当前网络。</div>`
    : "";
  const serverHint = `<div class="form-context"><span>协调服务</span><code>${srv ? esc(srv) : "官方 SnetServ"}</code></div>`;
  openModal({
    title: "创建网络",
    body: `<div class="network-form">
      ${hint}
      <div class="form-stack">
        <div class="form-field"><label for="m-name">网络名称</label><input id="m-name" class="form-control" type="text" placeholder="例如：家庭网络" maxlength="64" /></div>
        <div class="form-field"><label>虚拟网段</label>${subnetWidgetHTML({ id: "m-subnet", placeholder: "留空自动分配（如 10.88.0.0/24）", emptyHint: "留空自动分配，通常为 10.88.N.0/24" })}</div>
        <div class="form-toggle-row">
          <div><div class="form-toggle-title">新成员加入需要审批</div><div class="form-toggle-desc">开启后，加入请求会先出现在待审批列表，由你确认。</div></div>
          <label class="switch"><input id="m-approval" type="checkbox" /><span class="slider"></span></label>
        </div>
      </div>
      ${serverHint}
      <p class="msg" id="m-result"></p>
    </div>`,
    footer: `<button data-close class="btn ghost">取消</button><button id="m-submit" class="btn" ${hasOwner ? "disabled" : ""}>创建</button>`,
    onBody: (body) => {
      const submit = body.querySelector<HTMLButtonElement>("#m-submit")!;
      const result = body.querySelector<HTMLElement>("#m-result")!;
      const subnetWidget = subnetWidgetInit(body.querySelector<HTMLElement>(".subnet-wrap")!);
      submit.addEventListener("click", async () => {
        submit.disabled = true;
        submit.innerHTML = `${SPIN} 创建中…`;
        result.className = "msg";
        result.textContent = "";
        try {
          const name = (body.querySelector("#m-name") as HTMLInputElement).value.trim();
          const chk = subnetWidget.check();
          if (!chk.ok) throw new Error(`网段无效：${chk.error}`);
          const subnet = chk.value;
          const server = currentServer();
          if (!name) throw new Error("请输入网络名称");
          const approvalRequired = (body.querySelector("#m-approval") as HTMLInputElement)?.checked ?? false;
          const r: CreateResp = await backend.create({ server, port: s.wgport, name, subnet, approvalRequired });
          const modalEl = body;
          const mBody = modalEl.querySelector<HTMLElement>(".modal-body")!;
          const mFoot = modalEl.querySelector<HTMLElement>(".modal-foot")!;
          mBody.innerHTML = `
            <div class="create-ok">
              <div class="create-ok-main">
                <div class="create-ok-head"><span class="create-ok-icon">✓</span><div><b>网络创建成功</b><p>把邀请链接发给其他设备，扫码或粘贴即可加入。</p></div></div>
                <div class="invite-link-box">
                  <span class="invite-link-label">邀请链接</span>
                  <code class="invite-link">${esc(r.link)}</code>
                </div>
                <p class="hint">链接长期有效，建议只分享给信任的设备。</p>
              </div>
              <div class="create-ok-qr">
                <div class="qr" id="qr"></div>
                <p class="hint">扫码加入</p>
              </div>
            </div>`;
          renderQR(mBody.querySelector<HTMLElement>("#qr")!, r.link);
          detailNid = r.networkId;
          mFoot.innerHTML = `<button id="m-copy" class="btn">复制邀请链接</button><button id="m-close" class="btn ghost">关闭</button>`;
          mFoot.querySelector<HTMLButtonElement>("#m-copy")!.addEventListener("click", async (e) => {
            await copyText(r.link, e.currentTarget as HTMLButtonElement);
          });
          mFoot.querySelector<HTMLButtonElement>("#m-close")!.addEventListener("click", closeModal);
          await refresh();
        } catch (e) {
          result.className = "msg";
          result.textContent = `创建失败: ${e}`;
          submit.disabled = false;
          submit.innerHTML = "创建";
        }
      });
    },
  });
}

/* ── Join modal ───────────────────────────────────────────────── */
function linkServerOf(link: string): string | undefined {
  try {
    const u = new URL(link);
    if (u.protocol === "snet:" || u.protocol === "http:" || u.protocol === "https:") {
      return u.searchParams.get("server") || undefined;
    }
  } catch { /* not a URL */ }
  return undefined;
}

function openJoinModal() {
  const s = loadSettings();
  openModal({
    title: "加入网络",
    body: `<div class="join-compact">
      <label class="join-field-label" for="m-link">邀请链接</label>
      <div class="join-link-box">
        <input class="join-link-input" id="m-link" type="text" spellcheck="false" autocomplete="off"
               placeholder="粘贴 snet:// 邀请链接..." />
        ${backend.scanQR ? '<button id="m-scan" class="btn ghost" type="button" title="扫描二维码加入">扫码</button>' : ""}
      </div>
      <p class="hint join-tip">向网络创建者索取邀请链接，粘贴后点击「加入」。</p>
      <p class="msg" id="m-result"></p>
    </div>`,
    footer: `<button data-close class="btn ghost">取消</button><button id="m-submit" class="btn join-submit">加入</button>`,
    onBody: (body) => {
      const submit = body.querySelector<HTMLButtonElement>("#m-submit")!;
      const result = body.querySelector<HTMLElement>("#m-result")!;
      const linkInput = body.querySelector<HTMLInputElement>("#m-link")!;
      const scanBtn = body.querySelector<HTMLButtonElement>("#m-scan");

      // The join button only makes sense with a link in the field.
      const syncSubmit = () => { submit.disabled = !linkInput.value.trim(); };
      linkInput.addEventListener("input", syncSubmit);
      syncSubmit();

      // People copy the invite link right before pasting it, so offer the
      // clipboard content once the field gets the focus.
      let pasteTried = false;
      linkInput.addEventListener("focus", async () => {
        if (pasteTried || linkInput.value.trim()) return;
        pasteTried = true;
        try {
          if (!navigator.clipboard?.readText) return;
          const text = (await navigator.clipboard.readText()).trim();
          if (!text || !/^snet:\/\//i.test(text)) return;
          linkInput.value = text;
          syncSubmit();
          toast("已粘贴剪贴板中的邀请链接");
        } catch { /* clipboard read not permitted */ }
      });

      scanBtn?.addEventListener("click", async () => {
        if (!backend.scanQR) return;
        scanBtn.disabled = true;
        scanBtn.textContent = "扫描中…";
        try {
          const text = (await backend.scanQR()).trim();
          if (!/^snet:\/\/join\?/i.test(text)) {
            toast("不是有效的加入邀请链接", "err");
            return;
          }
          linkInput.value = text;
          syncSubmit();
          toast("已读取邀请链接");
          submit.focus();
        } catch (e) {
          toast(String(e), "err");
        } finally {
          scanBtn.disabled = false;
          scanBtn.textContent = "扫码";
        }
      });

      submit.addEventListener("click", async () => {
        submit.disabled = true;
        submit.innerHTML = `${SPIN} 加入中…`;
        result.className = "msg";
        result.textContent = "";
        try {
          const link = linkInput.value.trim();
          if (!link) throw new Error("请粘贴邀请链接");
          let inviteNID = "";
          try { inviteNID = new URL(link).searchParams.get("nid") || ""; } catch { /* backend validates */ }
          if (inviteNID && (status?.networks ?? []).some((n) => n.networkId === inviteNID && n.serverState !== "gone")) {
            throw new Error("该设备已加入此网络，请勿重复加入");
          }
          if (inviteNID && (status?.pendingJoins ?? []).some((p) => p.networkId === inviteNID && p.status !== "denied")) {
            throw new Error("已向该网络发起申请，请勿重复请求！");
          }
          const server = linkServerOf(link) || currentServer();
          const r: JoinResp = await backend.join({ server, port: s.wgport, link });
          result.className = "msg ok";
          if (r.status === "pending") {
            result.innerHTML = `<p>已提交加入请求，等待网络创建者批准。</p><p class="hint">批准后本机会自动加入并连接；也可在上方「待批准 · 加入请求」卡片中取消。</p>`;
          } else {
            result.textContent = `已加入，分配的地址为 ${r.ip ?? "-"}`;
            if (r.networkId) detailNid = r.networkId;
          }
          if (r.status === "pending" && r.pendingId) {
            detailPendingId = r.pendingId;
            detailNid = null;
            toast("加入请求已提交，等待网络创建者批准");
          }
          await refresh();
          closeModal();
        } catch (e) {
          result.className = "msg";
          result.textContent = `加入失败: ${e}`;
          submit.innerHTML = "加入";
          syncSubmit();
        }
      });
    },
  });
}

/* ── App settings modal ───────────────────────────────────────── */
function natViaLabel(via: string): string {
  if (via === "upnp") return "UPnP";
  if (via === "natpmp") return "NAT-PMP";
  if (via === "pcp") return "PCP";
  return via;
}

function openSettingsModal() {
  const s = loadSettings();
  const server = (status?.serverAddr || s.server || "").trim();
  const isOfficial = !server || server.replace(/\/+$/, "") === DEFAULT_SERVER.replace(/\/+$/, "");
  const nat = status?.natMapping;
  const natEnabled = nat?.enabled !== false;
  const natLines = (nat?.mappings ?? []).map(m =>
    `UDP ${m.port} → ${m.externalIp}:${m.externalPort} · ${natViaLabel(m.via)}`
  );
  const natErr = nat?.errors?.[0];
  // Relay switches: whether this device may act as a gateway, and whether it
  // honours the network's delegated routes (enabled by default).
  const relayGatewaySupported = status?.relayGatewaySupported !== false;
  const allowRelay = relayGatewaySupported && status?.allowRelay === true;
  const useRelay = status?.useRelay !== false;
  const relayErr = status?.relayError || "";
  openModal({
    title: "设置",
    body: `<div class="settings-shell">
      <section class="settings-panel">
        <div class="settings-panel-head"><h3>设备</h3><span class="hint">本机身份</span></div>
        <div class="settings-list">
          <div class="settings-row">
            <div class="settings-row-main">
              <div class="settings-row-title">设备 ID</div>
              <div class="settings-row-desc">用于在服务端和成员列表中识别这台设备</div>
            </div>
            <div class="settings-row-actions">
              <code class="settings-device-id" id="s-device-id">${esc(status?.deviceId || "-")}</code>
              <button type="button" id="s-copy-device" class="btn icon sm" title="复制设备 ID" aria-label="复制设备 ID">⧉</button>
            </div>
          </div>
        </div>
      </section>
      <section class="settings-panel">
        <div class="settings-panel-head"><h3>连接</h3><span class="hint">连接与端口</span></div>
        <div class="settings-list">
          <div class="settings-row">
            <div class="settings-row-main">
              <div class="settings-row-title">协调服务</div>
              <div class="settings-row-desc" id="s-server-label">${isOfficial ? "官方 (SnetServ)" : esc(server)}</div>
            </div>
            <div class="settings-row-actions" id="s-server-view">
              <button type="button" id="s-server-edit" class="btn icon sm" title="修改协调服务" aria-label="修改协调服务">
                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" width="15" height="15"><path d="M12 20h9"/><path d="M16.5 3.5a2.1 2.1 0 0 1 3 3L7 19l-4 1 1-4Z"/></svg>
              </button>
              ${isOfficial ? "" : '<button type="button" id="s-server-reset" class="btn ghost sm">恢复默认</button>'}
            </div>
          </div>
          <div id="s-server-editor" class="settings-editor" hidden>
            <input id="s-server" type="url" value="${isOfficial ? "" : esc(server)}" placeholder="https://my-vps:8090" autocomplete="off" />
            <p class="hint">留空则使用官方 SnetServ，或填入自托管地址。</p>
            <div class="settings-actions"><button id="s-server-save" class="btn">保存</button><button id="s-server-cancel" class="btn ghost">取消</button></div>
            <p class="msg" id="s-server-result"></p>
          </div>
          <div class="settings-row">
            <div class="settings-row-main">
              <div class="settings-row-title">自动端口映射</div>
              <div class="settings-row-desc">通过 UPnP / NAT-PMP / PCP 让对端直连，默认开启</div>
            </div>
            <label class="switch">
              <input id="s-nat" type="checkbox" ${natEnabled ? "checked" : ""} ${!status ? "disabled" : ""} />
              <span class="slider"></span>
            </label>
          </div>
          <p class="settings-note" id="s-nat-state">${!status ? "后台服务未运行" : !natEnabled ? "已关闭" : natLines.length ? `已映射 ${natLines.length} 个端口` : "正在等待路由器分配映射…"}</p>
          <div class="settings-note muted" id="s-nat-list">${natLines.map(esc).join("<br>")}${natErr ? `<div class="err">端口 ${natErr.port}: ${esc(natErr.error)}</div>` : ""}</div>
        </div>
      </section>
      <section class="settings-panel">
        <div class="settings-panel-head"><h3>中继路由</h3><span class="hint">本机网关与远程子网</span></div>
        <div class="settings-list">
          ${relayGatewaySupported
            ? `<div class="settings-row">
            <div class="settings-row-main">
              <div class="settings-row-title">允许本设备作为中继网关</div>
              <div class="settings-row-desc">为其他成员转发流量，需要 IP 转发与 NAT 权限</div>
            </div>
            <label class="switch">
              <input id="s-allow-relay" type="checkbox" ${allowRelay ? "checked" : ""} ${!status ? "disabled" : ""} />
              <span class="slider"></span>
            </label>
          </div>
          <p class="settings-note ${relayErr ? "err" : allowRelay ? "ok" : ""}" id="s-allow-relay-state">${relayErr ? esc(relayErr) : allowRelay ? "已开启，等待目标节点确认路由" : "已关闭"}</p>`
            : '<p class="settings-note muted">当前系统不支持作为中继网关；仍可使用网络内其他节点提供的中继路由。</p>'}
          <div class="settings-row">
            <div class="settings-row-main">
              <div class="settings-row-title">使用网络内的中继网关</div>
              <div class="settings-row-desc">按网络创建者委派的路由访问其他子网，默认开启</div>
            </div>
            <label class="switch">
              <input id="s-use-relay" type="checkbox" ${useRelay ? "checked" : ""} ${!status ? "disabled" : ""} />
              <span class="slider"></span>
            </label>
          </div>
          <p class="settings-note ${useRelay ? "ok" : ""}" id="s-use-relay-state">${useRelay ? "已开启" : "已关闭：忽略网络中所有已生效的中继路由"}</p>
        </div>
      </section>
      <section class="settings-panel">
        <div class="settings-panel-head"><h3>诊断</h3><span class="hint">日志与运行状态</span></div>
        <div class="settings-list">
          <div class="settings-row">
            <div class="settings-row-main">
              <div class="settings-row-title">诊断日志</div>
              <div class="settings-row-desc">导出最近的 daemon 日志用于排查</div>
            </div>
            <button id="s-logs" class="btn ghost sm" type="button">导出</button>
          </div>
          <div class="settings-row" id="s-daemon-row">
            <div class="settings-row-main">
              <div class="settings-row-title">后台服务</div>
              <div class="settings-row-desc">snetd 守护进程</div>
            </div>
            ${backend.hasDaemonControl ? (status ? '<span class="live-dot ok">运行中</span>' : isWailsWindows() ? `<span class="live-dot ${desktopDaemon.error ? "err" : ""}">${desktopDaemon.error ? "启动失败，请点击右上角重试" : "正在连接服务…"}</span>` : '<button id="s-start-daemon" class="btn ghost sm">启动</button>') : '<span class="live-dot ok">snetd</span>'}
          </div>
        </div>
      </section>
      <section class="settings-panel">
        <div class="settings-panel-head"><h3>关于</h3><span class="hint">Snet</span></div>
        <div class="settings-about">
          <p class="about-intro">Snet 是一个无需公网 IP、扫码即连的私有 WireGuard 网络，让设备之间优先直连，无法直连时自动中继。</p>
          <div class="about-meta">
            <span>作者 <b>S.shidi</b></span>
            <a href="mailto:s.shidi@gmail.com">s.shidi@gmail.com</a>
            <span>微信 <b>S-shidi</b></span>
          </div>
          <div class="about-version">
            <span>当前版本 <code id="about-version">v${esc(status?.version || "-")}</code></span>
            <span class="about-update" id="about-update">正在检查更新…</span>
            <a href="https://github.com/S-shidi/Snet/releases/latest" target="_blank" rel="noopener noreferrer">GitHub Releases ↗</a>
          </div>
        </div>
      </section>
      <details class="settings-advanced">
        <summary>高级：原始状态 JSON</summary>
        <pre class="settings-json">${esc(JSON.stringify(status ?? {}, null, 2))}</pre>
      </details>
      <p class="msg" id="s-msg"></p>
    `,
    wide: true,
    footer: `<button data-close class="btn ghost">关闭</button>`,
    onBody: (body) => {
      const msg = body.querySelector<HTMLElement>("#s-msg")!;
      const updateState = body.querySelector<HTMLElement>("#about-update");
      const currentVersion = String(status?.version || "");
      if (updateState && currentVersion) {
        fetch("https://api.github.com/repos/S-shidi/Snet/releases/latest", {
          headers: { Accept: "application/vnd.github+json" },
        })
          .then((r) => r.ok ? r.json() : Promise.reject(new Error(String(r.status))))
          .then((rel: { tag_name?: string }) => {
            const latest = String(rel.tag_name || "").replace(/^v/i, "");
            const norm = (v: string) => v.split(/[.-]/).map((x) => Number(x) || 0);
            const a = norm(currentVersion), b = norm(latest);
            let cmp = 0;
            for (let i = 0; i < Math.max(a.length, b.length); i++) {
              if ((a[i] || 0) !== (b[i] || 0)) { cmp = (a[i] || 0) > (b[i] || 0) ? 1 : -1; break; }
            }
            updateState.className = cmp < 0 ? "about-update new" : "about-update ok";
            updateState.textContent = latest ? (cmp < 0 ? `发现新版本 v${latest}` : "已是最新版本") : "无法检查更新";
          })
          .catch(() => {
            updateState.className = "about-update";
            updateState.textContent = "无法检查更新，请在 GitHub 查看最新版本";
          });
      }
      const serverView = body.querySelector<HTMLElement>("#s-server-view")!;
      const serverEditor = body.querySelector<HTMLElement>("#s-server-editor")!;
      const serverInput = body.querySelector<HTMLInputElement>("#s-server")!;
      const serverResult = body.querySelector<HTMLElement>("#s-server-result")!;
      const serverBtn = body.querySelector<HTMLButtonElement>("#s-server-save")!;
      const serverEdit = body.querySelector<HTMLButtonElement>("#s-server-edit");
      const serverCancel = body.querySelector<HTMLButtonElement>("#s-server-cancel");
      const serverReset = body.querySelector<HTMLButtonElement>("#s-server-reset");
      const showEditor = () => {
        serverView.hidden = true;
        serverEditor.hidden = false;
        serverResult.textContent = "";
        serverInput.focus();
      };
      serverEdit?.addEventListener("click", showEditor);
      serverCancel?.addEventListener("click", () => {
        serverEditor.hidden = true;
        serverView.hidden = false;
        serverResult.textContent = "";
      });

      const saveServer = async (value: string) => {
        serverBtn.disabled = true;
        serverResult.className = "msg";
        serverResult.textContent = "正在校验并保存…";
        try {
          if (value) {
            await backend.checkServer(value);
          }
          await backend.setServer(value);
          saveSettings({ ...loadSettings(), server: value || DEFAULT_SERVER });
          serverResult.className = "msg ok";
          serverResult.textContent = "已保存，正在使用新的 SnetServ";
          toast("SnetServ 已更新");
          await refresh();
          closeModal();
        } catch (e) {
          serverResult.className = "msg";
          serverResult.textContent = "无法连接该服务端，请检查地址";
          toast("无法连接该服务端，请检查地址", "err");
        } finally {
          serverBtn.disabled = false;
        }
      };

      serverBtn.addEventListener("click", () => saveServer(serverInput.value.trim()));
      serverReset?.addEventListener("click", () => saveServer(""));
      body.querySelector("#s-copy-device")?.addEventListener("click", async (e) => {
        await copyText(status?.deviceId || "", e.currentTarget as HTMLButtonElement);
      });

      const natInput = body.querySelector<HTMLInputElement>("#s-nat");
      const natState = body.querySelector<HTMLElement>("#s-nat-state");
      natInput?.addEventListener("change", async () => {
        if (!natState) return;
        const enabled = natInput.checked;
        natInput.disabled = true;
        natState.className = "settings-note";
        natState.textContent = enabled ? "正在开启…" : "正在关闭…";
        try {
          await backend.setNATMapping(enabled);
          await refresh();
          natState.className = "settings-note ok";
          natState.textContent = enabled ? "已开启自动端口映射" : "已关闭自动端口映射";
          toast(natState.textContent);
        } catch (e) {
          natInput.checked = !enabled;
          natState.className = "settings-note err";
          natState.textContent = `保存失败: ${e}`;
        } finally {
          natInput.disabled = false;
        }
      });

      // The two relay switches are saved together: the daemon keeps them in
      // one device-wide configuration and reports the gateway status back.
      const allowRelayInput = body.querySelector<HTMLInputElement>("#s-allow-relay");
      const allowRelayState = body.querySelector<HTMLElement>("#s-allow-relay-state");
      const useRelayInput = body.querySelector<HTMLInputElement>("#s-use-relay");
      const useRelayState = body.querySelector<HTMLElement>("#s-use-relay-state");

      const saveRelayOptions = async () => {
        const wantAllow = allowRelayInput?.checked ?? false;
        const wantUse = useRelayInput?.checked ?? true;
        if (allowRelayState) {
          allowRelayState.className = "settings-note";
          allowRelayState.textContent = wantAllow ? "正在开启中继网关…" : "正在关闭中继网关…";
        }
        if (allowRelayInput) allowRelayInput.disabled = true;
        if (useRelayInput) useRelayInput.disabled = true;
        try {
          await backend.setRelayOptions({ allowRelay: wantAllow, useRelay: wantUse });
          await refresh();
          if (allowRelayState) {
            allowRelayState.className = "settings-note ok";
            allowRelayState.textContent = wantAllow ? "已开启中继网关" : "已关闭中继网关";
          }
          if (useRelayState) {
            useRelayState.className = "settings-note ok";
            useRelayState.textContent = wantUse ? "已开启" : "已关闭：忽略网络中所有已生效的中继路由";
          }
          toast("中继设置已保存");
        } catch (e) {
          if (allowRelayInput) allowRelayInput.checked = !wantAllow;
          if (useRelayInput) useRelayInput.checked = !wantUse;
          if (allowRelayState) {
            allowRelayState.className = "settings-note";
            allowRelayState.textContent = `保存失败: ${e}`;
          }
        } finally {
          if (allowRelayInput) allowRelayInput.disabled = false;
          if (useRelayInput) useRelayInput.disabled = false;
        }
      };
      allowRelayInput?.addEventListener("change", () => { void saveRelayOptions(); });
      useRelayInput?.addEventListener("change", () => { void saveRelayOptions(); });

      body.querySelector("#s-logs")?.addEventListener("click", async (e) => {        const btn = e.currentTarget as HTMLButtonElement;
        btn.disabled = true;
        try {
          const text = await backend.logs();
          const blob = new Blob([text], { type: "text/plain;charset=utf-8" });
          const url = URL.createObjectURL(blob);
          const a = document.createElement("a");
          a.href = url;
          a.download = "snet-diagnostic.log";
          a.click();
          URL.revokeObjectURL(url);
          toast("诊断日志已导出");
        } catch (err) {
          toast(`导出日志失败: ${err}`, "err");
        } finally {
          btn.disabled = false;
        }
      });

      body.querySelector("#s-start-daemon")?.addEventListener("click", async () => {
        msg.className = "msg";
        msg.textContent = "正在启动…";
        try {
          await backend.ensureDaemon();
          msg.className = "msg ok";
          msg.textContent = "后台服务已就绪";
          await refresh();
          const row = body.querySelector("#s-daemon-row");
          if (row) row.innerHTML = '<span class="live-dot ok">后台服务: 运行中</span>';
        } catch (e) {
          msg.className = "msg"; msg.textContent = String(e);
        }
      });
    },
  });
}

/* ── Status tab ───────────────────────────────────────────────── */
/* ── Path monitor ─────────────────────────────────────────────── */
function fmtSpeed(bytesPerSec?: number): string {
  const n = Math.max(0, bytesPerSec || 0);
  if (n < 1024) return `${n.toFixed(0)} B/s`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB/s`;
  if (n < 1024 * 1024 * 1024) return `${(n / (1024 * 1024)).toFixed(1)} MB/s`;
  return `${(n / (1024 * 1024 * 1024)).toFixed(1)} GB/s`;
}

function fmtAgo(sec?: number): string {
  if (typeof sec !== "number" || sec < 0) return "未知";
  if (sec < 2) return "刚刚";
  if (sec < 60) return `${sec} 秒前`;
  if (sec < 3600) return `${Math.floor(sec / 60)} 分钟前`;
  return `${Math.floor(sec / 3600)} 小时前`;
}

function directKindLabel(pd?: PeerDirect): string {
  const kind = (pd?.kind || "").toLowerCase();
  if (kind === "lan") return "LAN";
  if (kind === "ipv6") return "IPv6";
  if (kind === "public") return "Public";
  const addr = pd?.addr || pd?.lan || pd?.ipv6 || pd?.public || "";
  if (addr.includes(":")) return "IPv6";
  if (/^(10\.|192\.168\.|172\.(1[6-9]|2\d|3[01])\.)/.test(addr)) return "LAN";
  return addr ? "Public" : "";
}

function effectivePath(pd: PeerDirect | undefined, relayReady: boolean): string {
  const path = pd?.path || "relay";
  if ((path === "discovering" || path === "probing") && relayReady) {
    const last = pd?.lastRecvSec;
    // A direct probe that has produced no peer traffic for the daemon's
    // 30s fallback window is stale; relay is the path actually in use.
    if (typeof last === "number" && last > 30) return "relay";
  }
  return path;
}

function pathBadge(pd: PeerDirect | undefined, relayReady: boolean): string {
  const path = effectivePath(pd, relayReady);
  if (path === "direct") {
    const kind = directKindLabel(pd);
    return `<span class="path-badge direct">直连${kind ? ` · ${esc(kind)}` : ""}</span>`;
  }
  if (path === "probing") return `<span class="path-badge probing">打洞中</span>`;
  if (path === "discovering") return `<span class="path-badge discovering">发现中</span>`;
  if (path === "relay") return `<span class="path-badge relay">中继</span>`;
  return `<span class="path-badge unknown">未知</span>`;
}

function pathAddr(pd?: PeerDirect): string {
  return pd?.addr || pd?.public || pd?.ipv6 || pd?.lan || "-";
}

function pathPeerRow(p: NetNode, pd: PeerDirect | undefined, relayReady: boolean): string {
  const last = typeof pd?.lastRecvSec === "number"
    ? `最后活跃 ${fmtAgo(pd.lastRecvSec)}`
    : "暂无活跃记录";
  return `<div class="path-peer">
    <div class="path-peer-main">
      <div class="path-peer-name">${deviceLabel(p)}</div>
      <div class="path-peer-meta">
        <span>虚拟 IP <code>${esc(p.ip || "-")}</code></span>
        <span>ID <code>${esc(p.id.slice(0, 8))}</code></span>
      </div>
    </div>
    <div class="path-peer-side">
      ${pathBadge(pd, relayReady)}
      <code class="path-peer-addr">${esc(pathAddr(pd))}</code>
      <span class="path-peer-last">${esc(last)}</span>
    </div>
  </div>`;
}

function isPrivateIPv4(ip: string): boolean {
  const s = String(ip || "").trim().replace(/^::ffff:/i, "");
  return /^(10\.|192\.168\.|172\.(1[6-9]|2\d|3[01])\.)/.test(s);
}

function hasCGNAT(): boolean {
  return (status?.natMapping?.mappings ?? []).some((m) => isPrivateIPv4(m.externalIp));
}

function pathMonitorHTML(n: NetInfo, r?: PeersResp, includeHead = true): { html: string; direct: number; relay: number; probing: number } {
  const self = r?.self;
  const selfIp = n.ip || self?.ip || "-";
  const nat = n.relayPath?.natType || self?.natType || "unknown";
  const directMap: Record<string, PeerDirect> = n.relayPath?.peerDirect ?? {};
  const relayReady = (n.relayPath?.relayReady ?? 0) > 0;
  const peers = r?.peers ?? [];
  let direct = 0;
  let relay = 0;
  let probing = 0;
  for (const p of peers) {
    const path = effectivePath(directMap[p.id], relayReady);
    if (path === "direct") direct++;
    else if (path === "relay") relay++;
    else probing++;
  }
  const p2p = direct > 0
    ? `<div class="path-p2p-banner"><span class="path-p2p-icon">↯</span><div><b>当前已建立 P2P 直连</b><span>${direct} 个节点正在通过直连传输，延迟更低。</span></div></div>`
    : "";
  const peerHTML = !r
    ? `<div class="path-empty">${peerErrors[n.networkId] ? "该网络已失效或加载失败" : "正在获取成员信息…"}</div>`
    : peers.length
      ? peers.map((p) => pathPeerRow(p, directMap[p.id], relayReady)).join("")
      : `<div class="path-empty">当前网络暂无其他节点。分享邀请链接或授权码即可组网。</div>`;
  const headHTML = includeHead
    ? `<div class="path-net-head">
        <div>
          <div class="path-net-name">${esc(n.name || n.networkId)}</div>
          <div class="path-net-id">${esc(n.networkId)}</div>
        </div>
        ${direct > 0 ? `<span class="path-p2p-pill">P2P ${direct}</span>` : ""}
      </div>`
    : "";
  const cgnatHTML = hasCGNAT()
    ? `<div class="path-cgnat-banner"><span class="path-cgnat-icon">!</span><div><b>检测到您的网络处于运营商多层 NAT（CGNAT）下</b><span>P2P 直连受限，已自动为您分配中继节点保证连通性。</span></div></div>`
    : "";
  return {
    html: `<div class="path-net">
      ${headHTML}
      ${cgnatHTML}
      <div class="path-self">
        <div class="path-self-row">
          <span class="path-label">本机 TUN IP</span>
          <code class="path-ip">${esc(selfIp)}</code>
          <span class="path-nat">NAT 类型 <b>${esc(nat)}</b></span>
        </div>
      </div>
      ${p2p}
      <div class="path-peers">${peerHTML}</div>
    </div>`,
    direct,
    relay,
    probing,
  };
}

function cgnatCompactHTML(): string {
  if (!hasCGNAT()) return "";
  return `<details class="cgnat-compact">
    <summary><span class="cgnat-mark">!</span><span>检测到运营商多层 NAT（CGNAT），已启用中继保障连通</span><span class="cgnat-more">详情</span></summary>
    <p>P2P 直连受限，客户端已自动选择中继路径；如需直连，请检查路由器 NAT 类型或 IPv6 连通性。</p>
  </details>`;
}

function memberPathBadge(n: NetInfo, nd: NetNode, isSelf: boolean): string {
  if (isSelf) return '<span class="pill off">本机</span>';
  const directMap: Record<string, PeerDirect> = n.relayPath?.peerDirect ?? {};
  const relayReady = (n.relayPath?.relayReady ?? 0) > 0;
  return pathBadge(directMap[nd.id], relayReady);
}

/* ── Network detail view ──────────────────────────────────────── */
function networkRole(n: NetInfo): string {
  // The status card's owner flag is authoritative. The peer snapshot can be
  // stale (or omit the self role), and must not downgrade the owner to member.
  if (n.owner) return "owner";
  const self = peerDetails[n.networkId]?.self;
  const role = String(self?.role || n.role || "member").trim().toLowerCase();
  return role === "owner" ? "owner" : "member";
}

function canManageNetwork(n: NetInfo): boolean {
  return networkRole(n) === "owner" || !!n.owner;
}

function lastActiveLabel(pd: PeerDirect | undefined, online: boolean): string {
  const sec = pd?.lastRecvSec;
  if (typeof sec !== "number" || sec < 0) return "暂无活跃记录";
  if (!online && sec < 2) return "最后活跃 1 秒前";
  return `最后活跃 ${fmtAgo(sec)}`;
}

function pendingBannersHTML(nid: string, pending: NetInfoDetail["pending"]): string {
  if (!pending?.length) return "";
  // A device can retry while an older request is still pending. Keep only the
  // newest request per device so the owner sees one row per physical device.
  const latest = new Map<string, NonNullable<NetInfoDetail["pending"]>[number]>();
  for (const p of pending) {
    const key = p.deviceId || p.publicKey || p.id;
    const cur = latest.get(key);
    if (!cur || String(p.createdAt || "") > String(cur.createdAt || "")) latest.set(key, p);
  }
  return [...latest.values()].map((p) => {
    const label = p.deviceName || p.deviceId || p.publicKey.slice(0, 12);
    return `<div class="pending-request-banner">
      <span class="pending-request-mark">!</span>
      <span class="pending-request-text"><b>${esc(label)}</b> 请求加入网络，等待你的批准</span>
      <span class="pending-request-actions">
        <button type="button" class="btn ghost sm" data-pending-deny="${esc(p.id)}" data-pending-nid="${esc(nid)}">拒绝</button>
        <button type="button" class="btn sm" data-pending-approve="${esc(p.id)}" data-pending-nid="${esc(nid)}">批准</button>
      </span>
    </div>`;
  }).join("");
}

function memberCardsHTML(n: NetInfo, r: PeersResp | undefined, canManage: boolean, selfRole: string, routes: RelayRoute[] | undefined): string {
  if (!r) return `<p class="hint member-empty">${peerErrors[n.networkId] ? "该网络已失效或加载失败" : "正在获取成员信息…"}</p>`;
  const self = r.self;
  const list: NetNode[] = (self ? [self, ...(r.peers ?? [])] : (r.peers ?? []))
    .slice()
    .sort((a, b) => {
      const aSelf = !!self && a.id === self.id ? 0 : 1;
      const bSelf = !!self && b.id === self.id ? 0 : 1;
      return aSelf - bSelf || String(a.id).localeCompare(String(b.id));
    });
  if (!list.length) return `<p class="hint member-empty">暂无成员信息</p>`;
  const directMap: Record<string, PeerDirect> = n.relayPath?.peerDirect ?? {};
  const relayReady = (n.relayPath?.relayReady ?? 0) > 0;

  // A stopped network has no live tunnel, so peer snapshots from before the
  // disconnect must not keep rendering "online"/"relay" labels.
  const connected = n.active === true && n.serverState !== "gone";
  const cards = list.map((nd) => {
    const isSelf = nd.ip === n.ip || (!!self && nd.id === self.id);
    const online = connected && !!nd.online;
    const pd = directMap[nd.id];
    const path = isSelf
      ? '<span class="path-badge self">本机</span>'
      : online ? pathBadge(pd, relayReady) : "";
    const routeCell = nodeRouteCell(routes, nd.id);
    const relay = nd.allowRelay
      ? '<span class="pill ok">中继网关</span>'
      : nd.relayError
        ? `<span class="pill warn" title="${esc(nd.relayError)}">网关不可用</span>`
        : "";
    const role = String(isSelf ? selfRole : (nd.role || "member")).trim().toLowerCase();
    const roleText = nd.isHub ? "中继" : roleLabel(role);
    const hubName = nd.isHub
      ? '<span class="hub-name"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" width="14" height="14"><path d="M17.5 19a4.5 4.5 0 0 0 .5-8.97A6.5 6.5 0 0 0 5.2 12.2 3.5 3.5 0 0 0 6.5 19h11Z"/></svg> 官方中继</span>'
      : deviceLabel(nd);
    const public4 = isSelf
      ? (nd.publicIPv4 || nd.endpoint || "-")
      : (pd?.public || pd?.addr || pd?.lan || nd.endpoint || "-");
    const selfV6 = nd.publicIPv6 || nd.endpointV6 || "";
    const public6 = isSelf ? (selfV6 || "未检测到IPv6信息") : (pd?.ipv6 || nd.endpointV6 || "-");
    const showPublic6 = isSelf ? true : public6 !== "-" && public6 !== public4;
    const last = isSelf
      ? (connected ? "当前设备" : "已断开")
      : (connected ? lastActiveLabel(pd, online) : "未连接");
    const poke = isSelf
      ? ""
      : `<button type="button" class="btn ghost sm member-poke" data-detail-poke="${esc(nd.id)}" data-poke-target="${esc(nd.ip || "")}" title="发送探测包唤醒节点"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" width="14" height="14"><path d="M13 2 4 14h7l-1 8 9-12h-7l1-8Z"/></svg>唤醒</button>`;
    const diag = isSelf
      ? ""
      : `<button type="button" class="btn ghost sm" data-detail-ping="${esc(nd.id)}" data-ping-target="${esc(nd.ip || "")}">诊断</button>`;
    const wgExport = nd.deviceId?.startsWith("wg-") ? wgExports[n.networkId]?.[nd.id] : undefined;
    const wgConfig = canManage && wgExport
      ? `<button type="button" class="btn ghost sm" data-wg-config="${esc(nd.id)}" data-wg-name="${esc(nd.deviceName || "WireGuard 客户端")}">配置</button>`
      : "";
    const kick = !isSelf && canManage
      ? `<button type="button" class="btn danger ghost sm" data-detail-kick="${esc(nd.id)}" ${nd.isHub ? 'data-hub="1"' : ""}>${nd.isHub ? "移除" : "踢出"}</button>`
      : "";
    const renameIcon = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" width="14" height="14"><path d="M12 20h9"/><path d="M16.5 3.5a2.1 2.1 0 0 1 3 3L7 19l-4 1 1-4Z"/></svg>';
    const rename = nd.isHub || (!canManage && !isSelf)
      ? ""
      : isSelf
        ? `<button type="button" class="member-edit" data-detail-rename="${esc(nd.deviceName || "")}" title="重命名设备" aria-label="重命名设备">${renameIcon}</button>`
        : `<button type="button" class="member-edit" data-detail-rename-node="${esc(nd.id)}" data-rename-name="${esc(nd.deviceName || "")}" data-rename-nid="${esc(n.networkId)}" title="重命名设备" aria-label="重命名设备">${renameIcon}</button>`;
    const editIP = canManage && !nd.isHub
      ? `<button type="button" class="member-edit" data-detail-edit-ip="${esc(nd.id)}" data-edit-ip-nid="${esc(n.networkId)}" data-current-ip="${esc(nd.ip || "")}" title="修改虚拟 IP" aria-label="修改虚拟 IP">${renameIcon}</button>`
      : "";
    const foot = `<div class="member-card-foot">
      <span class="member-card-last">${esc(last)}</span>
      <span class="member-card-actions">${wgConfig}${poke}${diag}${kick}</span>
    </div>`;
    return `<article class="member-card${isSelf ? " self" : ""}${online ? "" : " offline"}" data-member-id="${esc(nd.id)}">
      <div class="member-card-head">
        <div class="member-card-name"><span class="member-name-text">${hubName}</span>${rename}</div>
        <div class="member-card-status">${path}${!connected ? `<span class="pill off">${isSelf ? "已断开" : "未连接"}</span>` : online ? '<span class="pill ok">在线</span>' : '<span class="pill off">离线</span>'}</div>
      </div>
      <div class="member-card-body">
        <div class="member-card-line"><span class="member-card-label">虚拟 IP</span><code>${esc(nd.ip || "-")}</code>${editIP}</div>
        <div class="member-card-line"><span class="member-card-label">身份</span><span class="member-card-value">${roleText}</span></div>
        <div class="member-card-line member-card-public"><span class="member-card-label">公网 IP</span><span class="member-card-value"><code class="member-endpoint">${esc(public4)}</code>${showPublic6 ? `<code class="member-endpoint member-endpoint-v6">${esc(public6)}</code>` : ""}</span></div>
        <div class="member-card-line"><span class="member-card-label">子网路由</span><span class="member-card-value">${routeCell}${relay}</span></div>
      </div>
      ${foot}
    </article>`;
  }).join("");
  return `<div class="member-grid">${cards}</div>`;
}

function openRenameDeviceModal(current: string, target?: { nid: string; nodeId: string }) {
  openModal({
    title: "重命名设备",
    body: `<div class="row"><label>设备名称</label><input id="rename-device-name" type="text" maxlength="64" value="${esc(current)}" placeholder="例如：办公室 Mac" autocomplete="off" /></div>
      <p class="hint">名称会同步到所有已加入网络的成员列表。</p>
      <p class="msg" id="rename-device-result"></p>`,
    footer: `<button data-close class="btn ghost">取消</button><button id="rename-device-save" class="btn">保存</button>`,
    onBody: (body) => {
      const input = body.querySelector<HTMLInputElement>("#rename-device-name")!;
      const result = body.querySelector<HTMLElement>("#rename-device-result")!;
      const btn = body.querySelector<HTMLButtonElement>("#rename-device-save")!;
      const save = async () => {
        const name = input.value.trim();
        if (!name) { result.textContent = "设备名称不能为空"; return; }
        btn.disabled = true;
        result.className = "msg";
        result.textContent = "保存中…";
        try {
          if (target) await backend.renameNodeDevice({ nid: target.nid, nodeId: target.nodeId, name });
          else await backend.renameDevice(name);
          toast("设备名称已更新");
          closeModal();
          await refreshAfterDeviceChange();
        } catch (e) {
          result.className = "msg";
          result.textContent = `保存失败: ${e}`;
          btn.disabled = false;
        }
      };
      btn.addEventListener("click", () => void save());
      input.addEventListener("keydown", (e) => { if (e.key === "Enter") void save(); });
      setTimeout(() => input.focus(), 30);
    },
  });
}

function openEditIPModal(nid: string, nodeId: string, current: string) {
  const net = (status?.networks ?? []).find((n) => n.networkId === nid) ?? ownerInfo[nid];
  const subnet = net?.subnet || "";
  const parts = subnet.split("/");
  const prefix = Number(parts[1]);
  const base = (parts[0] || "").split(".").map((v) => Number(v));
  if (parts.length !== 2 || !Number.isInteger(prefix) || prefix < 8 || prefix > 24 || base.length !== 4 || base.some((v) => !Number.isInteger(v))) {
    openModal({
      title: "修改虚拟 IP",
      body: `<p class="msg">无法读取当前网络网段，已禁止编辑虚拟 IP。</p>`,
      footer: `<button data-close class="btn ghost">关闭</button>`,
    });
    return;
  }
  const currentParts = current.split(".").map((v) => Number(v));
  const maskOctet = (i: number): number => {
    const bits = prefix - i * 8;
    if (bits >= 8) return 255;
    if (bits <= 0) return 0;
    return (0xff << (8 - bits)) & 0xff;
  };
  const octets = [0, 1, 2, 3].map((i) => {
    const mask = maskOctet(i);
    const fixed = base[i] & mask;
    let min = fixed;
    let max = fixed + (255 - mask);
    // The last octet cannot be the network or broadcast address.
    if (i === 3) { min = Math.max(min, 1); max = Math.min(max, 254); }
    const initial = Number.isInteger(currentParts[i]) && currentParts[i] >= min && currentParts[i] <= max
      ? currentParts[i]
      : (mask === 255 ? base[i] : min);
    return { mask, min, max, initial };
  });
  const segment = (i: number) => {
    if (octets[i].mask === 255) {
      return `<span class="ip-locked" title="由网络网段固定">${octets[i].initial}</span>`;
    }
    return `<input class="ip-octet" inputmode="numeric" pattern="[0-9]*" maxlength="3" value="${octets[i].initial}" data-index="${i}" data-min="${octets[i].min}" data-max="${octets[i].max}" aria-label="IP 第 ${i + 1} 段（${octets[i].min}-${octets[i].max}）" />`;
  };
  openModal({
    title: "修改虚拟 IP",
    body: `<div class="ip-edit-row">
        <label>虚拟 IP</label>
        <div class="ip-segmented">${segment(0)}<span>.</span>${segment(1)}<span>.</span>${segment(2)}<span>.</span>${segment(3)}</div>
      </div>
      <p class="hint">当前网段 <code>${esc(subnet)}</code>：灰色段由网段固定，只能编辑高亮数字段。</p>
      <p class="hint">IP 必须位于该网段内，且不能与其他成员重复。</p>
      <p class="msg" id="edit-node-ip-result"></p>`,
    footer: `<button data-close class="btn ghost">取消</button><button id="edit-node-ip-save" class="btn">保存</button>`,
    onBody: (body) => {
      const inputs = Array.from(body.querySelectorAll<HTMLInputElement>(".ip-octet"));
      const result = body.querySelector<HTMLElement>("#edit-node-ip-result")!;
      const btn = body.querySelector<HTMLButtonElement>("#edit-node-ip-save")!;

      const normalize = (input: HTMLInputElement) => {
        input.value = input.value.replace(/\D/g, "").slice(0, 3);
      };
      inputs.forEach((input, idx) => {
        input.addEventListener("input", () => {
          normalize(input);
          if (input.value.length >= 3 && idx < inputs.length - 1) inputs[idx + 1].focus();
        });
        input.addEventListener("keydown", (e) => {
          if (e.key === ".") {
            e.preventDefault();
            if (idx < inputs.length - 1) inputs[idx + 1].focus();
          } else if (e.key === "Backspace" && input.value === "" && idx > 0) {
            e.preventDefault();
            inputs[idx - 1].focus();
          } else if (e.key === "ArrowLeft" && input.selectionStart === 0 && idx > 0) {
            inputs[idx - 1].focus();
          } else if (e.key === "ArrowRight" && input.selectionStart === input.value.length && idx < inputs.length - 1) {
            inputs[idx + 1].focus();
          }
        });
        input.addEventListener("paste", (e) => {
          const text = (e.clipboardData?.getData("text") || "").trim();
          const pasted = text.split(".").map((v) => Number(v));
          if (!/^\d{1,3}(\.\d{1,3}){3}$/.test(text) || pasted.some((v) => v > 255)) return;
          e.preventDefault();
          for (const input of inputs) {
            const idx = Number(input.dataset.index);
            input.value = String(pasted[idx] ?? "");
          }
          inputs[inputs.length - 1]?.focus();
        });
      });

      const readIP = (): string => {
        const values = octets.map((o, i) => {
          const input = body.querySelector<HTMLInputElement>(`.ip-octet[data-index="${i}"]`);
          if (!input) return o.mask === 255 ? o.initial : o.min;
          const n = Number(input.value.trim());
          if (input.value.trim() === "" || !Number.isInteger(n)) throw new Error("请填写完整的 IPv4 地址");
          if (n < o.min || n > o.max) {
            throw new Error(`第 ${i + 1} 段必须在 ${o.min}–${o.max} 之间`);
          }
          return n;
        });
        return values.join(".");
      };

      const save = async () => {
        try {
          const ip = readIP();
          btn.disabled = true;
          result.className = "msg"; result.textContent = "保存中…";
          try {
            await backend.setNodeIP({ nid, nodeId, ip });
            toast("虚拟 IP 已更新");
            closeModal();
            await refreshAfterDeviceChange();
          } catch (e) {
            result.className = "msg"; result.textContent = `保存失败: ${String(e).replace(/^Error:\s*/, "").replace(/^server \d+:\s*/, "")}`;
            btn.disabled = false;
          }
        } catch (e) {
          result.className = "msg"; result.textContent = String(e);
        }
      };
      btn.addEventListener("click", () => void save());
      const first = inputs[0];
      if (first) setTimeout(() => first.focus(), 30);
    },
  });
}

function showPingModal(text: string, cls: string) {
  openModal({
    title: "诊断结果",
    body: `<div class="ping-modal ${esc(cls)}"><span class="ping-modal-icon">${cls === "ok" ? "✓" : "!"}</span><div><div class="ping-modal-title">${cls === "ok" ? "连接正常" : "诊断失败"}</div><div class="ping-modal-text">${esc(text)}</div></div></div>`,
    footer: `<button data-close class="btn ghost">关闭</button>`,
  });
}

async function runPeerPing(btn: HTMLButtonElement) {
  const target = btn.dataset.pingTarget || "";
  if (!target) {
    showPingModal("诊断失败：无 IP", "err");
    return;
  }
  const original = btn.innerHTML;
  btn.disabled = true;
  btn.innerHTML = `${SPIN} 诊断中…`;
  try {
    const r = await backend.ping(target);
    const loss = Math.max(0, Number(r.loss) || 0);
    const avg = Math.max(0, Number(r.avgRtt) || 0);
    if (loss >= 100) throw new Error("目标不可达");
    const cls = loss >= 50 || avg >= 200 ? "err" : loss > 0 || avg >= 80 ? "warn" : "ok";
    showPingModal(`延迟: ${avg > 0 ? `${avg.toFixed(0)}ms` : "-"} / 丢包: ${loss.toFixed(0)}%`, cls);
  } catch (e) {
    // Report the real reason: a missing ping binary, an unreachable target
    // and an auth failure all need different fixes.
    const msg = String(e).replace(/^Error:\s*/, "") || "未知错误";
    showPingModal(`诊断失败：${msg}`, "err");
    toast(`诊断失败：${msg}`, "err");
  } finally {
    btn.disabled = false;
    btn.innerHTML = original;
  }
}

async function runPoke(btn: HTMLButtonElement) {
  const target = btn.dataset.pokeTarget || "";
  if (!target) {
    toast("唤醒失败：目标 IP 为空", "err");
    return;
  }
  const original = btn.innerHTML;
  btn.disabled = true;
  btn.innerHTML = `${SPIN} 唤醒中…`;
  toast("正在尝试唤醒该节点…");
  try {
    await backend.poke(target);
    await new Promise((resolve) => window.setTimeout(resolve, 1200));
    await refresh();
    toast("已发送唤醒探测包");
  } catch (e) {
    toast(`唤醒失败：${e}`, "err");
  } finally {
    btn.disabled = false;
    btn.innerHTML = original;
  }
}

/* ── Invite link helpers ───────────────────────────────────────── */
// The invite link is the only artifact a member ever needs. When the server
// can no longer rebuild it (it stores the pairing code hashed only, so a
// restart loses the plaintext), a fresh link is issued instead.
async function inviteInfo(nid: string): Promise<{ link: string; name?: string } | null> {
  const r = await ctlOrClean(nid, "邀请加入", async () => {
    try {
      return await backend.inviteLink(nid);
    } catch (e) {
      if (!String(e).includes("失效")) throw e;
      const fresh = await backend.rotateInvite(nid);
      toast("原邀请链接已失效，已生成新链接", "warn");
      return fresh;
    }
  });
  if (r === null) return null;
  return { link: r.link, name: r.name };
}

async function leaveNetworkFromDetail(nid: string) {
  if (!(await confirmDialog("退出网络", "退出后将断开该网络并移除本地网络配置，确定继续？", true))) return;
  try {
    const r = await ctlOrClean(nid, "退出网络", () => backend.remove(nid));
    if (r === null) return;
    toast("已退出网络");
    closeNetworkDetail();
    await refresh();
  } catch (e) {
    toast(String(e), "err");
  }
}

async function openNetworkDetailModal(nid: string) {
  try {
    const r = await backend.netinfo(nid);
    // The pairing code is never exposed to the user: redact it from the
    // raw debug view.
    const safe = { ...r, pairingCode: r.pairingCode ? "***" : "" };
    openModal({
      title: "网络详情",
      wide: true,
      body: `<pre class="json">${esc(JSON.stringify(safe, null, 2))}</pre>`,
      footer: `<button data-close class="btn ghost">关闭</button>`,
    });
  } catch (e) {
    toast(`获取网络详情失败: ${e}`, "err");
  }
}

async function deleteNetworkFromDetail(nid: string, btn: HTMLButtonElement) {
  if (!(await confirmDialog("删除网络", "删除将断开所有成员并释放网段，不可恢复。确定？", true))) return;
  const orig = btn.textContent;
  btn.disabled = true;
  btn.textContent = "删除中…";
  try {
    const r = await ctlOrClean(nid, "删除网络", () => backend.deleteNet(nid));
    if (r === null) return;
    toast("网络已删除");
    closeNetworkDetail();
    await refresh();
  } catch (e) {
    toast(String(e), "err");
    btn.disabled = false;
    btn.textContent = orig;
  }
}

async function refreshWireGuardExports(force = false): Promise<void> {
  if (!force && !status) return;
  const ownerNets = (status?.networks ?? []).filter((n) => n.owner);
  await Promise.all(ownerNets.map(async (n) => {
    try {
      const items = await withTimeout(backend.wireguardExports(n.networkId), 5000, "WireGuard 配置加载超时");
      const m: Record<string, WireGuardExport> = {};
      for (const e of items) m[e.nodeId] = e;
      wgExports[n.networkId] = m;
    } catch {
      // Keep the last successful map; the configuration lives in the daemon
      // and will be retried on the next refresh.
    }
  }));
}

function wireGuardConfigHTML(exp: WireGuardExport, name: string): string {
  const created = exp.createdAt ? new Date(exp.createdAt).toLocaleString() : "-";
  return `<div class="wg-config-view">
    <div class="wg-config-meta">
      <div><span>设备</span><b>${esc(name || "WireGuard 客户端")}</b></div>
      <div><span>网关</span><b>${esc(exp.gatewayName || exp.gatewayNodeId || "-")}</b></div>
      <div><span>生成时间</span><b>${esc(created)}</b></div>
    </div>
    <div class="wg-export-result">
      <div class="wg-export-text">
        <div class="wg-export-head"><b>WireGuard 配置</b><button type="button" class="btn ghost sm" id="wg-copy">复制</button></div>
        <textarea id="wg-config" readonly spellcheck="false"></textarea>
      </div>
      <div class="wg-export-qr"><div class="qr wg-config-qr" id="wg-qr"></div><p class="hint">在 WireGuard App 内扫码导入</p><button type="button" class="btn ghost sm" id="wg-qr-zoom">放大二维码</button></div>
    </div>
    <p class="hint">私钥只保存在本机守护进程的配置目录中；如已卸载客户端，请重新生成配置。</p>
  </div>`;
}

function openWireGuardConfigModal(nid: string, nodeID: string, exp?: WireGuardExport, name = "") {
  const found = exp ?? wgExports[nid]?.[nodeID];
  if (!found) {
    toast("该设备的 WireGuard 配置不在本机缓存中", "err");
    return;
  }
  openModal({
    title: `WireGuard 配置 · ${name || "设备"}`,
    wide: true,
    body: wireGuardConfigHTML(found, name),
    footer: `<button data-close class="btn ghost">关闭</button><button id="wg-copy-foot" class="btn">复制配置</button>`,
    onBody: (body) => {
      const cfg = found.config;
      const area = body.querySelector<HTMLTextAreaElement>("#wg-config")!;
      area.value = cfg;
      renderQR(body.querySelector<HTMLElement>("#wg-qr")!, cfg);
      const copy = async (btn: HTMLButtonElement) => { await copyText(cfg, btn); };
      body.querySelector<HTMLButtonElement>("#wg-qr-zoom")?.addEventListener("click", () => {
        const overlay = document.createElement("div");
        overlay.className = "qr-zoom-backdrop";
        overlay.innerHTML = '<div class="qr-zoom-panel"><button type="button" class="qr-zoom-close" aria-label="关闭">×</button><div class="qr" id="wg-qr-zoom-canvas"></div></div>';
        document.body.appendChild(overlay);
        renderQR(overlay.querySelector<HTMLElement>("#wg-qr-zoom-canvas")!, cfg);
        const close = () => overlay.remove();
        overlay.addEventListener("click", (e) => { if (e.target === overlay) close(); });
        overlay.querySelector<HTMLButtonElement>(".qr-zoom-close")?.addEventListener("click", close);
      });
      body.querySelector<HTMLButtonElement>("#wg-copy")?.addEventListener("click", (e) => void copy(e.currentTarget as HTMLButtonElement));
      body.querySelector<HTMLButtonElement>("#wg-copy-foot")?.addEventListener("click", (e) => void copy(e.currentTarget as HTMLButtonElement));
    },
  });
}

async function openWireGuardExportModal(nid: string) {
  let gateways: GatewayInfo[] = [];
  try {
    gateways = await backend.gateways(nid);
  } catch (e) {
    toast(`加载网关失败: ${e}`, "err");
    return;
  }
  if (!gateways.length) {
    toast("当前没有可用的 WireGuard 网关", "err");
    return;
  }
  const options = gateways.map((g) => {
    const suffix = g.isHub && !g.joined ? " [未加入，选择后自动接入]" : "";
    return `<option value="${esc(g.id)}">${esc(g.name)}${suffix}</option>`;
  }).join("");
  const preferred = gateways.find((g) => g.isHub && g.canBeGateway) ?? gateways.find((g) => g.isHub) ?? gateways[0];
  openModal({
    title: "生成 WireGuard 配置",
    wide: true,
    body: `<div class="form-stack">
        <div class="form-field">
          <label for="wg-gateway">出口网关</label>
          <select id="wg-gateway" class="form-control">${options}</select>
        </div>
        <p class="hint">配置将通过该节点的公网入口接入网络。官方 Hub 未加入时会自动接入。</p>
        <p class="msg" id="wg-gateway-state"></p>
      </div>`,
    footer: `<button data-close class="btn ghost">取消</button><button id="wg-generate" class="btn" disabled>生成配置</button>`,
    onBody: (body) => {
      const select = body.querySelector<HTMLSelectElement>("#wg-gateway")!;
      const state = body.querySelector<HTMLElement>("#wg-gateway-state")!;
      const generate = body.querySelector<HTMLButtonElement>("#wg-generate")!;
      select.value = preferred.id;
      const update = () => {
        const g = gateways.find((x) => x.id === select.value);
        if (!g) {
          state.className = "msg";
          state.textContent = "";
          generate.disabled = true;
          return;
        }
        state.className = g.canBeGateway ? "msg ok" : "msg err";
        state.textContent = g.canBeGateway ? "✓ 该节点具备公网入口，可以生成配置。" : `✗ 无法生成：${g.reason || "该节点不可用"}`;
        generate.disabled = !g.canBeGateway;
      };
      select.addEventListener("change", update);
      update();
      generate.addEventListener("click", async () => {
        generate.disabled = true;
        generate.innerHTML = `${SPIN} 生成中…`;
        try {
          const g = gateways.find((x) => x.id === select.value);
          const resp = await backend.wireguardConfig(nid, select.value);
          const exp: WireGuardExport = {
            nodeId: resp.nodeId,
            networkId: nid,
            gatewayNodeId: select.value,
            gatewayName: g?.name || "",
            config: resp.config,
            createdAt: new Date().toISOString(),
          };
          if (!wgExports[nid]) wgExports[nid] = {};
          wgExports[nid][resp.nodeId] = exp;
          closeModal();
          void refresh();
          openWireGuardConfigModal(nid, resp.nodeId, exp, "WireGuard 客户端");
        } catch (e) {
          generate.disabled = false;
          generate.textContent = "生成配置";
          state.className = "msg err";
          state.textContent = `生成失败：${e}`;
        }
      });
    },
  });
}

function networkDetailHTML(n: NetInfo): string {
  const r = peerDetails[n.networkId];
  const self = r?.self;
  const role = networkRole(n);
  const canManage = canManageNetwork(n);
  const selfIp = n.ip || self?.ip || "-";
  const nat = n.relayPath?.natType || self?.natType || "unknown";
  const routes = ownerInfo[n.networkId]?.routes ?? r?.routes ?? [];
  const activeRoutes = routes.filter((rt) => rt.status === "active").length;
  const memberCount = r ? (r.peers ?? []).length + 1 : 0;
  const rolePill = role === "owner"
    ? '<span class="pill owner">创建者</span>'
    : '<span class="pill off">成员</span>';
  const iconInvite = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" width="15" height="15"><path d="M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2"/><circle cx="9" cy="7" r="4"/><path d="M19 8v6"/><path d="M16 11h6"/></svg>';
  const iconMore = '<svg viewBox="0 0 24 24" fill="currentColor" width="15" height="15"><circle cx="5" cy="12" r="1.7"/><circle cx="12" cy="12" r="1.7"/><circle cx="19" cy="12" r="1.7"/></svg>';
  const more = `<div class="detail-more">
    <button type="button" class="btn ghost sm" id="detail-more-btn">${iconMore}更多</button>
    <div class="detail-more-menu" id="detail-more-menu" hidden>
      <button type="button" data-detail-act="detail">网络详情</button>
      ${canManage ? '<button type="button" data-detail-act="wg-export">生成 WireGuard 配置</button>' : ""}
      ${canManage ? '<button type="button" data-detail-act="settings">编辑网络</button>' : ""}
      ${canManage ? '<button type="button" data-detail-act="subnets">子网路由管理</button>' : ""}
      ${canManage ? "" : '<button type="button" data-detail-act="leave">退出网络</button>'}
      ${canManage ? '<button type="button" class="danger" data-detail-act="delete">删除网络</button>' : ""}
    </div>
  </div>`;
  return `<div class="net-detail-head">
    <div class="detail-heading">
      <div class="detail-title-row"><div class="detail-title">${esc(n.name || n.networkId)}</div>${rolePill}</div>
      <div class="detail-meta-line">
        <span class="detail-net-id">网络 ID <code>${esc(n.networkId)}</code></span>
        <span>↑ ${fmtSpeed(status?.txSpeed)} · ↓ ${fmtSpeed(status?.rxSpeed)}</span>
        <span>NAT ${esc(nat)}</span>
        <span>${activeRoutes} 条生效路由</span>
        ${memberCount ? `<span>${memberCount} 位成员</span>` : ""}
      </div>
    </div>
    <div class="detail-head-actions">
      <label class="switch detail-conn-switch" title="连接 / 断开">
        <input id="detail-net-toggle" type="checkbox" ${n.active ? "checked" : ""} ${n.serverState === "gone" ? "disabled" : ""} />
        <span class="slider"></span>
      </label>
      ${canManage ? `<button type="button" class="btn ghost sm" id="detail-invite" data-detail-act="invite">${iconInvite}邀请</button>` : ""}
      ${more}
    </div>
  </div>
  ${canManage ? pendingBannersHTML(n.networkId, ownerInfo[n.networkId]?.pending) : ""}
  ${cgnatCompactHTML()}
  ${memberCardsHTML(n, r, canManage, role, routes)}`;
}

function pendingDetailHTML(p: NonNullable<DaemonStatus["pendingJoins"]>[number]): string {
  return `<div class="pending-state-card">
    <div class="pending-state-icon">…</div>
    <div class="pending-state-main">
      <div class="pending-state-title">加入请求待审批</div>
      <p>已向网络创建者提交加入请求。批准后本机将自动加入网络并建立连接。</p>
      <div class="pending-state-meta"><span>网络</span><code>${esc(p.networkId)}</code><span>状态</span><span class="pill warn">等待批准</span></div>
      <div class="pending-state-actions">
        <button type="button" class="btn danger ghost sm" data-detail-act="cancel-pending">取消加入请求</button>
      </div>
    </div>
  </div>`;
}

function renderNetworkDetail() {
  const box = $("#net-detail");
  if (!box) return;
  if (detailPendingId) {
    const p = (status?.pendingJoins ?? []).find((x) => x.pendingId === detailPendingId);
    if (!p) {
      box.hidden = true;
      return;
    }
    box.hidden = false;
    box.innerHTML = pendingDetailHTML(p);
    return;
  }
  if (!detailNid) {
    box.hidden = true;
    return;
  }
  if (!status) {
    box.hidden = false;
    box.innerHTML = `<div class="net-detail-head">
      <div class="detail-heading"><div class="detail-title">网络详情</div></div>
    </div><p class="muted">后台服务未运行，无法读取网络详情</p>`;
    return;
  }
  const n = (status.networks ?? []).find((x) => x.networkId === detailNid);
  if (!n) {
    closeNetworkDetail();
    return;
  }
  box.hidden = false;
  box.innerHTML = networkDetailHTML(n);
}

async function openNetworkDetail(nid: string) {
  const n = (status?.networks ?? []).find((x) => x.networkId === nid);
  if (!n || n.serverState === "gone") return;
  detailPendingId = null;
  detailNid = nid;
  renderNetworkTabs(sortedNetworks());
  const detail = $("#net-detail");
  if (detail) detail.hidden = false;
  renderNetworkDetail();
  try {
    const [peers, info] = await Promise.all([
      withTimeout(backend.peers(nid), 5000, "成员信息加载超时"),
      backend.netinfo(nid),
    ]);
    peerDetails[nid] = peers;
    ownerInfo[nid] = info;
    delete peerErrors[nid];
    renderNetworkDetail();
  } catch {
    peerErrors[nid] = "该网络已失效或加载失败";
    renderNetworkDetail();
  }
}

function openPendingDetail(pid: string) {
  const p = (status?.pendingJoins ?? []).find((x) => x.pendingId === pid);
  if (!p) return;
  detailNid = null;
  detailPendingId = pid;
  renderNetworkTabs(sortedNetworks());
  renderNetworkDetail();
}

function closeNetworkDetail() {
  detailNid = null;
  detailPendingId = null;
  const detail = $("#net-detail");
  if (detail) {
    detail.hidden = true;
    detail.innerHTML = "";
  }
  renderNetworks();
}

function bindNetworkDetailEvents() {
  const detail = $("#net-detail");
  if (!detail) return;
  detail.addEventListener("click", async (e) => {
    const target = e.target as HTMLElement;
    const moreBtn = target.closest<HTMLButtonElement>("#detail-more-btn");
    if (moreBtn) {
      e.stopPropagation();
      const menu = document.getElementById("detail-more-menu");
      if (menu) menu.hidden = !menu.hidden;
      return;
    }
    const moreAction = target.closest<HTMLElement>(".detail-more-menu [data-detail-act]");
    if (moreAction) {
      const menu = document.getElementById("detail-more-menu");
      if (menu) menu.hidden = true;
    }
    const pendingCancel = target.closest<HTMLElement>('[data-detail-act="cancel-pending"]');
    if (pendingCancel && detailPendingId) {
      const pid = detailPendingId;
      if (!(await confirmDialog("取消加入请求", "确定取消等待批准？", true))) return;
      try {
        await backend.cancelPending(pid);
        toast("已取消加入请求");
        detailPendingId = null;
        await refresh();
      } catch (err) {
        toast(String(err), "err");
      }
      return;
    }
    const pendingAction = target.closest<HTMLButtonElement>("[data-pending-approve], [data-pending-deny]");
    if (pendingAction && detailNid) {
      const pid = pendingAction.dataset.pendingApprove || pendingAction.dataset.pendingDeny || "";
      const nid = pendingAction.dataset.pendingNid || detailNid;
      const deny = !!pendingAction.dataset.pendingDeny;
      if (!pid) return;
      const original = pendingAction.innerHTML;
      pendingAction.disabled = true;
      pendingAction.innerHTML = `${SPIN} ${deny ? "拒绝中…" : "批准中…"}`;
      try {
        if (deny) await backend.deny({ nid, pendingId: pid });
        else await backend.approve({ nid, pendingId: pid });
        toast(deny ? "已拒绝加入请求" : "已批准加入");
        // Bypass the peer/owner caches so the new member card and the removed
        // pending banner update together.
        await refreshAfterDeviceChange();
      } catch (e) {
        toast(String(e), "err");
        pendingAction.disabled = false;
        pendingAction.innerHTML = original;
      }
      return;
    }
    const renameEl = target.closest<HTMLButtonElement>("[data-detail-rename]");
    if (renameEl) {
      openRenameDeviceModal(renameEl.dataset.detailRename || "");
      return;
    }
    const renameNodeEl = target.closest<HTMLButtonElement>("[data-detail-rename-node]");
    if (renameNodeEl) {
      openRenameDeviceModal(renameNodeEl.dataset.renameName || "", {
        nid: renameNodeEl.dataset.renameNid || detailNid || "",
        nodeId: renameNodeEl.dataset.detailRenameNode || "",
      });
      return;
    }
    const editIPEl = target.closest<HTMLButtonElement>("[data-detail-edit-ip]");
    if (editIPEl) {
      openEditIPModal(editIPEl.dataset.editIpNid || detailNid || "", editIPEl.dataset.detailEditIp || "", editIPEl.dataset.currentIp || "");
      return;
    }
    const pokeEl = target.closest<HTMLButtonElement>("[data-detail-poke]");
    if (pokeEl && detailNid) {
      await runPoke(pokeEl);
      return;
    }
    const pingEl = target.closest<HTMLButtonElement>("[data-detail-ping]");
    if (pingEl && detailNid) {
      await runPeerPing(pingEl);
      return;
    }
    const wgBtn = (e.target as HTMLElement).closest<HTMLButtonElement>("[data-wg-config]");
    if (wgBtn && detailNid) {
      openWireGuardConfigModal(detailNid, wgBtn.dataset.wgConfig || "", undefined, wgBtn.dataset.wgName || "");
      return;
    }
    const el = (e.target as HTMLElement).closest<HTMLElement>("[data-detail-act], [data-detail-kick]");
    if (!el || !detailNid) return;
    const nid = detailNid;
    if (el.dataset.detailKick) {
      const isHub = el.dataset.hub === "1";
      const title = isHub ? "移除官方 Hub" : "移除成员";
      const message = isHub
        ? "移除官方 Hub 后，通过该 Hub 接入的第三方设备将断开连接，确定吗？"
        : "移除该成员？该设备将立即断开。";
      if (!(await confirmDialog(title, message, true))) return;
      const btn = el as HTMLButtonElement;
      const orig = btn.textContent;
      btn.disabled = true;
      btn.textContent = "移除中…";
      try {
        const r = await ctlOrClean(nid, "移除成员", () => backend.kick({ nid, nodeId: el.dataset.detailKick! }));
        if (r === null) return;
        toast("已移除成员");
        delete peerDetails[nid];
        await refreshPeerSubnets(true);
        await refresh();
      } catch (err) {
        toast(String(err), "err");
        btn.disabled = false;
        btn.textContent = orig;
      }
      return;
    }
    // Every action reports its own failure: a rejected promise here would
    // leave the user with a button that does nothing.
    try {
      switch (el.dataset.detailAct) {
        case "detail": await openNetworkDetailModal(nid); break;
        case "wg-export": await openWireGuardExportModal(nid); break;
        case "invite": await openInviteModal(nid); break;
        case "settings": await openNetworkSettings(nid); break;
        case "subnets": await openSubnetRouteModal(nid); break;
        case "rotate-invite": await rotateInviteLink(nid); break;
        case "leave": await leaveNetworkFromDetail(nid); break;
        case "delete": await deleteNetworkFromDetail(nid, el as HTMLButtonElement); break;
      }
    } catch (err) {
      toast(String(err), "err");
    }
  });
  document.addEventListener("click", (e) => {
    if (!(e.target as HTMLElement).closest(".detail-more")) {
      const menu = document.getElementById("detail-more-menu");
      if (menu) menu.hidden = true;
    }
  });
  detail.addEventListener("change", async (e) => {
    const input = e.target as HTMLInputElement;
    if (input.id !== "detail-net-toggle" || !detailNid) return;
    input.disabled = true;
    try {
      const r = await ctlOrClean(detailNid, input.checked ? "连接网络" : "断开网络",
        () => input.checked ? backend.rejoin(detailNid!) : backend.leave(detailNid!));
      if (r === null) return;
      toast(input.checked ? "已连接" : "已断开");
      await refresh();
    } catch (err) {
      toast(String(err), "err");
      input.checked = !input.checked;
    } finally {
      input.disabled = false;
    }
  });
}

function renderStatus() {
  const deviceId = $("#device-id");
  const svcWgport = $("#svc-wgport");
  const svcNat = $("#svc-nat");
  const statusJson = $("#status-json");
  if (!status) {
    if (deviceId) deviceId.textContent = "-";
    if (svcWgport) svcWgport.textContent = "-";
    if (svcNat) svcNat.textContent = "-";
    if (statusJson) statusJson.textContent = "(未连接后台服务)";
    return;
  }
  if (deviceId) deviceId.textContent = status.deviceId || "-";
  const emptyDeviceId = $("#empty-device-id");
  if (emptyDeviceId) emptyDeviceId.textContent = status.deviceId || "-";
  if (svcWgport) svcWgport.textContent = String(status.wgPort ?? "-");
  const nets = status.networks ?? [];
  if (svcNat) {
    const types = [...new Set(nets.map((n) => n.relayPath?.natType).filter(Boolean))];
    svcNat.textContent = types.length ? types.join(" / ") : "未知";
  }
  if (statusJson) {
    const display = { ...status, serverAddr: isOfficialServer(status.serverAddr || "") ? "SnetServ" : status.serverAddr };
    statusJson.textContent = JSON.stringify(display, null, 2);
  }
}

/* ── Desktop daemon supervisor ────────────────────────────────── */
async function pollDesktopDaemon(): Promise<void> {
  if (!isWailsWindows() || !backend.daemonStatus) return;
  if (desktopPollTimer !== undefined) {
    window.clearTimeout(desktopPollTimer);
    desktopPollTimer = undefined;
  }
  try {
    const st = await backend.daemonStatus();
    desktopDaemon = { running: !!st.running, starting: !!st.starting, error: st.error || "" };
    renderHeader();
    if (st.running) {
      await refresh();
      return;
    }
    if (st.error) return;
    desktopPollTimer = window.setTimeout(() => void pollDesktopDaemon(), 1000);
  } catch {
    desktopPollTimer = window.setTimeout(() => void pollDesktopDaemon(), 1500);
  }
}

async function retryDesktopDaemon(): Promise<void> {
  if (!isWailsWindows()) return;
  desktopDaemon = { running: false, starting: true, error: "" };
  renderHeader();
  try {
    await backend.ensureDaemon();
  } catch {
    // /desktop/status carries the actionable error.
  }
  await pollDesktopDaemon();
}

function startDesktopDaemonWatch(): void {
  if (!isWailsWindows()) return;
  void pollDesktopDaemon();
}

/* ── Ensure daemon ────────────────────────────────────────────── */
async function ensureDaemon() {
  const btn = $("#header-svc-btn") as HTMLButtonElement;
  if (btn) { btn.disabled = true; btn.textContent = "正在初始化系统服务…"; }
  try {
    await backend.ensureDaemon();
    toast("后台服务已就绪");
  } catch (e) {
    toast(String(e), "err");
  } finally {
    if (btn) btn.disabled = false;
    await refresh();
  }
}

/* ── Event bindings ───────────────────────────────────────────── */
function bindEvents() {
  $("#btn-info")?.addEventListener("click", openSettingsModal);
  $("#empty-create")?.addEventListener("click", openCreateModal);
  $("#empty-join")?.addEventListener("click", openJoinModal);
  $("#network-tabs")?.addEventListener("click", (e) => {
    const target = e.target as HTMLElement;
    if (target.closest("#btn-add-network")) {
      openNetworkChooser();
      return;
    }
    const pendingTab = target.closest<HTMLElement>("[data-pid]");
    if (pendingTab?.dataset.pid) {
      openPendingDetail(pendingTab.dataset.pid);
      return;
    }
    const tab = target.closest<HTMLElement>("[data-nid]");
    if (tab?.dataset.nid) void openNetworkDetail(tab.dataset.nid);
  });
  $("#btn-create")?.addEventListener("click", openCreateModal);
  $("#btn-join")?.addEventListener("click", openJoinModal);
  $("#btn-refresh")?.addEventListener("click", async () => { await refresh(); toast("已刷新"); });
  $("#btn-leave-all")?.addEventListener("click", async () => {
    try {
      await backend.leave("");
      toast("已停止全部网络");
    } catch (e) {
      toast(String(e), "err");
    }
    await refresh();
  });

  // Mobile header dropdown
  const btnMore = $("#btn-more") as HTMLButtonElement | null;
  const dropdown = $("#header-dropdown") as HTMLElement | null;
  if (btnMore && dropdown) {
    btnMore.addEventListener("click", (e) => {
      e.stopPropagation();
      dropdown.hidden = !dropdown.hidden;
    });
    document.addEventListener("click", () => { dropdown.hidden = true; });
    dropdown.querySelectorAll<HTMLElement>("[data-action]").forEach((item) => {
      item.addEventListener("click", () => {
        dropdown.hidden = true;
        const action = item.dataset.action;
        if (action === "refresh") void refresh();
        else if (action === "info") openSettingsModal();
        else if (action === "logout") { /* logout handled in adapter */ window.dispatchEvent(new Event("snet-logout")); }
      });
    });
  }
}

/* ── Init ─────────────────────────────────────────────────────── */
export async function init(b: Backend) {
  backend = b;
  await loadQR();
  initWailsWindowDrag();
  initTabs();
  bindNetListEvents();
  bindNetworkDetailEvents();
  bindEvents();
  // Hide logout button on Desktop/Android (not needed without login)
  if (backend.hasDaemonControl) {
    document.documentElement.classList.add("wails-desktop", isWailsWindows() ? "wails-windows" : "wails-macos");
    const lo = $("#btn-logout") as HTMLElement | null;
    if (lo) lo.style.display = "none";
    document.querySelectorAll<HTMLElement>("[data-action=\"logout\"]").forEach((el) => { el.style.display = "none"; });
  }
  startDesktopDaemonWatch();
  refresh();
  setInterval(() => {
    if (!document.hidden && !refreshing) refresh();
  }, 5000);
}
