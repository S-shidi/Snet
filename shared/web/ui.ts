/* ── Shared UI logic for SNET client ──────────────────────────────
 * This module is the single source of truth for the client UI.
 * Platform adapters (Desktop/Web/Android) provide a Backend implementation
 * and call init() to wire everything up.
 */
import { DEFAULT_SERVER, type Backend, type DaemonStatus, type NetInfoDetail, type PeersResp, type CreateResp, type JoinResp, type PeerDirect, type NetInfo, type NetNode } from "./types.js";
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
let detailNid: string | null = null;
let detailPendingId: string | null = null;
let lastNetHTML = "";
let lastEmptyHTML = "";
let lastTabsHTML = "";
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
      await refreshOwnerInfo();
      await refreshPeerSubnets();
    } catch {
      status = null;
    }
    renderHeader();
    renderNetworks();
    renderNetworkDetail();
    renderStatus();
  } while (refreshAgain);
  refreshing = false;
}

/* ── Owner info cache ─────────────────────────────────────────── */
const ownerInfo: Record<string, NetInfoDetail> = {};
let lastOwnerFetch = 0;
async function refreshOwnerInfo(force = false) {
  const now = Date.now();
  if (!force && now - lastOwnerFetch < 10000) return;
  lastOwnerFetch = now;
  if (!status) return;
  const ownerNets = (status.networks ?? []).filter((n) => n.owner);
  await Promise.all(
    ownerNets.map(async (n) => {
      try {
        ownerInfo[n.networkId] = await backend.netinfo(n.networkId);
      } catch {
        delete ownerInfo[n.networkId];
      }
    }),
  );
}

/* ── Peer advertised subnets (any member) ─────────────────────── */
const peerAllowed: Record<string, string[]> = {};
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
        const set = new Set<string>();
        for (const p of list) (p.allowedSubnets ?? []).forEach((s) => set.add(s));
        for (const s of n.allowedSubnets ?? []) set.add(s);
        peerAllowed[n.networkId] = [...set].sort();
      } catch (e) {
        delete peerAllowed[n.networkId];
        delete peerSummary[n.networkId];
        delete peerDetails[n.networkId];
        peerErrors[n.networkId] = "该网络已失效或加载失败";
      }
    }),
  );
}

/* ── Header ───────────────────────────────────────────────────── */
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
    if (status) {
      dot.className = "live-dot ok";
      dot.textContent = "运行中";
    } else {
      dot.className = "live-dot err";
      dot.textContent = "未运行";
    }
    const btn = $("#header-svc-btn") as HTMLButtonElement;
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
function netCard(n: { networkId: string; name?: string; ip?: string; subnet?: string; interface?: string; active?: boolean; owner?: boolean; serverState?: string; error?: string; state?: string; peerStats?: Record<string, { RxBytes?: number; TxBytes?: number; LastHandshakeSec?: number }>; allowedSubnets?: string[]; visibility?: string; description?: string; tags?: string[]; role?: string }): string {
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
  const allSubnets = peerAllowed[n.networkId] ?? n.allowedSubnets ?? [];
  return `
  <div class="net" data-nid="${esc(n.networkId)}" data-owner="${n.owner ? "1" : "0"}" ${gone ? 'data-gone="1"' : ""}>
    <div class="net-row">
      <div class="net-main">
        <div class="net-name">${esc(n.name || n.networkId)} ${n.owner ? `<span class="pill owner">owner</span>` : ""} ${sharePill} ${pendingPill}</div>
        <div class="net-meta">
          <span>IP <code>${esc(n.ip ?? "-")}</code></span>
          <span>网段 <code>${esc(n.subnet ?? "-")}</code></span>
          <span>ID <code>${esc(n.networkId)}</code></span>
          ${allSubnets.length > 0 ? `<span class="pill subnet-route" title="可路由：${esc(allSubnets.join(", "))}">路由 ${allSubnets.length} 个子网</span>` : ""}
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
  const iconCode = '<svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="16 18 22 12 16 6"/><polyline points="8 6 2 12 8 18"/></svg>';
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
      <button type="button" data-act="code" data-nid="${esc(nid)}">${iconCode} 查看配对码</button>
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
  const html = nets.map((n) => {
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
  openModal({
    title: "添加网络",
    body: `<div class="network-choice">
      <button type="button" class="network-choice-card" id="choice-create">
        <span class="choice-icon">+</span>
        <b>创建新网络</b>
        <small>创建一个新的虚拟网络并邀请设备加入</small>
      </button>
      <button type="button" class="network-choice-card" id="choice-join">
        <span class="choice-icon">→</span>
        <b>加入现有网络</b>
        <small>使用邀请链接或配对码加入已有网络</small>
      </button>
    </div>`,
    footer: `<button data-close class="btn ghost">取消</button>`,
    onBody: (body) => {
      body.querySelector("#choice-create")?.addEventListener("click", () => {
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
  const nets = sortedNetworks();
  const pending = status?.pendingJoins ?? [];
  const hasContent = !!status && (nets.length > 0 || pending.length > 0);
  const empty = $("#empty-state");
  const main = $("#app-main");
  if (empty) empty.hidden = hasContent;
  if (main) main.hidden = !hasContent;

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
        const r = await backend.netinfo(nid);
        openModal({ title: `网络详情 · ${nid}`, body: `<pre class="json">${esc(JSON.stringify(r, null, 2))}</pre>`, wide: true });
        break;
      }
      case "members": { setPending("查询中…"); await showMembers(nid); break; }
      case "settings": { await openNetworkSettings(nid); break; }
      case "subnets": { await openSubnetRouteModal(nid); break; }
      case "invite": { setPending("生成中…"); await openInviteModal(nid); break; }
      case "code": { setPending("查询中…"); await openCodeModal(nid); break; }
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
    body: `
      <div class="row"><label>网络名称</label><input id="s-name" type="text" value="${esc(curName)}" placeholder="留空保持不变" /></div>
      <div class="row"><label>网段</label>${subnetWidgetHTML({ id: "s-subnet", value: curSubnet, placeholder: "留空保持不变", emptyHint: "留空保持不变" })}</div>
      <div class="row"><label class="inline"><input id="s-approval" type="checkbox" ${detail.approvalRequired ? "checked" : ""} /> 新成员加入需创建者批准</label></div>
      <p class="msg" id="s-warn" hidden>修改网段会重新分配所有成员 IP，已加入的 SNET 客户端会自动重连；但手机等外部 WireGuard 设备需手动重新导入新配置。</p>
      <p class="msg" id="s-result"></p>`,
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
  const curSubnets: string[] = net?.allowedSubnets ?? [];

  const renderTags = (tags: string[]) =>
    tags.length
      ? tags.map((s) => `<span class="chip chip-active">${esc(s)}<button data-del="${esc(s)}" class="chip-del" title="移除">&times;</button></span>`).join("")
      : `<span class="muted">暂无生效子网</span>`;

  openModal({
    title: `子网路由 · ${nid}`,
    body: `
      <p class="hint">宣告本设备的局域网子网，其他成员可通过 VPN 访问。</p>
      <div class="subnet-section subnet-section-active">
        <div class="subnet-section-title">当前生效的子网</div>
        <div id="sr-tags" class="subnet-tags subnet-tags-active">${renderTags(curSubnets)}</div>
      </div>
      <div class="subnet-section subnet-section-detected">
        <div class="subnet-section-title">检测到的本地子网</div>
        <div id="sr-suggest" class="subnet-tags subnet-tags-detected"><span class="muted">正在检测…</span></div>
      </div>
      <div class="subnet-section subnet-section-manual">
        <div class="subnet-section-title">手动添加</div>
        <div class="subnet-add-row">
          <input type="text" id="sr-input" placeholder="如 192.168.3.0/24" />
          <button id="sr-add" class="btn ghost sm">添加</button>
        </div>
      </div>
      <p class="msg" style="margin-top:8px">IP 转发将由后台服务自动开启，无需手动配置。</p>
      <p class="msg" id="sr-result"></p>`,
    footer: `<button data-close class="btn ghost">取消</button><button id="sr-save" class="btn">保存</button>`,
    onBody: (body) => {
      const tags = [...curSubnets];
      const tagsEl = body.querySelector<HTMLElement>("#sr-tags")!;
      const suggestEl = body.querySelector<HTMLElement>("#sr-suggest")!;
      const input = body.querySelector<HTMLInputElement>("#sr-input")!;
      const result = body.querySelector<HTMLElement>("#sr-result")!;

      const rerender = () => { tagsEl.innerHTML = renderTags(tags); };

      const refreshSuggest = (localSubnets: string[]) => {
        const suggestions = localSubnets.filter((s) => !tags.includes(s));
        if (!suggestions.length) {
          suggestEl.innerHTML = `<span class="muted">未发现其他可用的本地子网</span>`;
          return;
        }
        suggestEl.innerHTML = suggestions
          .map((s) => `<span class="chip chip-candidate" data-add="${esc(s)}" title="添加 ${esc(s)}">${esc(s)} <span class="chip-add">+</span></span>`)
          .join("");
      };

      let localSubnets: string[] = [];
      backend.detectLocalSubnets()
        .then((arr) => { localSubnets = arr; refreshSuggest(arr); })
        .catch(() => { refreshSuggest([]); });

      suggestEl.addEventListener("click", (e) => {
        const chip = (e.target as HTMLElement).closest<HTMLElement>("[data-add]");
        if (!chip) return;
        const cidr = chip.dataset.add!;
        if (tags.includes(cidr)) return;
        tags.push(cidr);
        rerender();
        refreshSuggest(localSubnets);
      });

      body.querySelector("#sr-add")!.addEventListener("click", () => {
        const raw = input.value.trim();
        if (!raw) return;
        if (tags.includes(raw)) { result.textContent = "该子网已添加"; return; }
        const chk = subnetCheck(raw, 24);
        if (!chk.ok) { result.className = "msg"; result.textContent = `无效: ${chk.error}`; return; }
        const cidr = chk.value;
        if (tags.includes(cidr)) { result.textContent = "该子网已添加"; return; }
        tags.push(cidr);
        input.value = "";
        result.textContent = "";
        rerender();
        refreshSuggest(localSubnets);
      });
      input.addEventListener("keydown", (e) => {
        if (e.key === "Enter") { e.preventDefault(); (body.querySelector("#sr-add") as HTMLElement).click(); }
      });
      tagsEl.addEventListener("click", (e) => {
        const btn = (e.target as HTMLElement).closest<HTMLButtonElement>("[data-del]");
        if (!btn) return;
        const val = btn.dataset.del!;
        const idx = tags.indexOf(val);
        if (idx >= 0) tags.splice(idx, 1);
        rerender();
        refreshSuggest(localSubnets);
      });

      body.querySelector("#sr-save")!.addEventListener("click", async () => {
        const saveBtn = body.querySelector<HTMLButtonElement>("#sr-save")!;
        const origText = saveBtn.textContent;
        saveBtn.disabled = true;
        result.className = "msg";
        result.textContent = "保存中…";
        try {
          const r = await ctlOrClean(nid, "子网路由", async () => {
            await backend.updateSubnets({ nid, subnets: tags });
            return true;
          });
          if (r === null) return;
          result.className = "msg ok";
          result.textContent = "已保存";
          toast("子网路由已更新");
          closeModal();
        } catch (e) {
          result.className = "msg";
          result.textContent = `保存失败: ${e}`;
          saveBtn.disabled = false;
          saveBtn.textContent = origText;
        }
      });
    },
  });
}

/* ── Code modal ───────────────────────────────────────────────── */
async function openCodeModal(nid: string) {
  let code = "";
  try {
    const detail = await backend.netinfo(nid);
    code = detail.pairingCode || "";
  } catch (e) {
    toast(`获取配对码失败: ${e}`, "err");
    return;
  }
  if (!code) {
    const nr = await ctlOrClean(nid, "查看配对码", () => backend.resetCode(nid));
    if (nr === null) return;
    code = nr.pairingCode;
    toast("原配对码不可用，已生成新码（旧码作废）", "warn");
  }
  openModal({
    title: `查看配对码 · ${nid}`,
    body: `
      <div class="kv"><span>配对码</span><code id="pc-code" style="letter-spacing:1.5px">${esc(code)}</code></div>
      <p class="hint" id="pc-note">配对码含有效次数与时限；复制后分享给其他设备即可加入。</p>
      <p class="msg" id="pc-result"></p>`,
    footer: `<button data-close class="btn ghost">关闭</button><button id="pc-copy" class="btn">复制配对码</button><button id="pc-reset" class="btn danger">重置配对码</button>`,
    onBody: (m) => {
      const codeEl = m.querySelector<HTMLElement>("#pc-code")!;
      const result = m.querySelector<HTMLElement>("#pc-result")!;
      const note = m.querySelector<HTMLElement>("#pc-note")!;
      const copy = m.querySelector<HTMLButtonElement>("#pc-copy")!;
      const reset = m.querySelector<HTMLButtonElement>("#pc-reset")!;
      copy.addEventListener("click", () => void copyText(code, copy));
      reset.addEventListener("click", async () => {
        if (!(await confirmDialog("重置配对码", "作废旧配对码并生成新码？旧码立即失效，已加入成员不受影响。", true))) return;
        reset.disabled = true;
        reset.innerHTML = `${SPIN} 生成中…`;
        try {
          const r = await ctlOrClean(nid, "重置配对码", () => backend.resetCode(nid));
          if (r === null) { reset.disabled = false; reset.textContent = "重置配对码"; return; }
          code = r.pairingCode;
          codeEl.textContent = code;
          result.className = "msg ok";
          result.textContent = "旧码已失效，已加入成员不受影响。";
          note.textContent = "";
          toast("已生成新配对码");
        } catch (e) {
          result.className = "msg";
          result.textContent = `重置失败: ${e}`;
        } finally {
          reset.disabled = false;
          reset.textContent = "重置配对码";
        }
        await refresh();
      });
    },
  });
}

/* ── Invite modal ─────────────────────────────────────────────── */
async function openInviteModal(nid: string) {
  let detail: NetInfoDetail;
  try {
    detail = await backend.netinfo(nid);
  } catch (e) {
    toast(`获取邀请信息失败: ${e}`, "err");
    return;
  }
  let code = detail.pairingCode;
  if (!code) {
    const nr = await ctlOrClean(nid, "邀请加入", () => backend.resetCode(nid));
    if (nr === null) return;
    code = nr.pairingCode;
    toast("原配对码不可用，已生成新码（旧码作废）", "warn");
  }
  const netId = detail.id || detail.networkId;
  if (!netId) { toast("获取网络ID失败", "err"); return; }
  const name = detail.name || netId;
  let link = `snet://join?nid=${encodeURIComponent(netId)}&code=${encodeURIComponent(code)}`;
  if (name) link += `&name=${encodeURIComponent(name)}`;
  openModal({
    title: `邀请加入 · ${name}`,
    wide: true,
    body: `
      <p class="hint">被邀请设备扫描下方二维码，或在 App 中选择「加入网络」粘贴邀请链接即可加入。</p>
      <div class="qr" id="qr"></div>
      <div class="kv"><span>邀请链接</span><code>${esc(link)}</code></div>
      <div class="kv"><span>网络ID</span><code>${esc(netId)}</code></div>
      <div class="kv"><span>配对码</span><code>${esc(code)}</code></div>
      <p class="hint">配对码含有效次数与时限，可在卡片上「查看配对码」随时查看或作废重发。</p>`,
    footer: `<button data-close class="btn ghost">关闭</button><button id="m-copy" class="btn">复制邀请链接</button>`,
    onBody: (body) => {
      renderQR(body.querySelector<HTMLElement>("#qr")!, link);
      body.querySelector("#m-copy")?.addEventListener("click", async (e) => {
        await copyText(link, e.currentTarget as HTMLButtonElement);
      });
    },
  });
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
        <td><code>${p.deviceId ? esc(p.deviceId.slice(0, 12)) + "…" : "-"}</code></td>
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
  const id = nd.deviceId ? nd.deviceId.slice(0, 8) : "";
  if (name && id) return `${esc(name)} <span class="muted mono">(${esc(id)})</span>`;
  if (name) return esc(name);
  if (id) return `<span class="muted mono">${esc(id)}</span>`;
  return "-";
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
      const subnets = (nd.allowedSubnets?.length ?? 0) > 0
        ? `<span class="pill subnet-route" title="子网路由：${esc(nd.allowedSubnets!.join(", "))}">${esc(nd.allowedSubnets!.join(", "))}</span>`
        : `<span class="muted">-</span>`;
      return `<tr><td class="mono">${esc(nd.ip)}</td><td>${deviceLabel(nd)}</td><td>${online}</td><td>${roleCtl}</td><td>${subnets}</td><td>${kick}</td></tr>`;
    }).join("");
    const pendingRows = (r.pending ?? []).map((p) => {
      return `<tr><td colspan="2"><span class="muted">设备</span> <code>${p.deviceId ? esc(p.deviceId.slice(0, 8)) + "…" : "-"}</code><span class="muted"> 公钥</span> <code>${esc(p.publicKey.slice(0, 12))}…</code></td><td><span class="pill warn">待批准</span></td><td>-</td><td><button data-pend="${esc(p.id)}" class="btn sm" style="background:var(--ok)">批准</button> <button data-pend="${esc(p.id)}" class="btn danger sm">拒绝</button></td></tr>`;
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
  const r = await backend.peers(nid);
  const all = r.self ? [r.self, ...(r.peers ?? [])] : (r.peers ?? []);
  const rows = all.map((nd) => {
    const isSelf = nd.ip === myIp;
    const online = nd.online ? '<span class="pill ok">在线</span>' : '<span class="pill off">离线</span>';
    const subnets = (nd.allowedSubnets?.length ?? 0) > 0
        ? `<span class="pill subnet-route" title="子网路由：${esc(nd.allowedSubnets!.join(", "))}">${esc(nd.allowedSubnets!.join(", "))}</span>`
        : `<span class="muted">-</span>`;
    return `<tr><td class="mono">${esc(nd.ip)}</td><td>${deviceLabel(nd)}</td><td>${online}</td><td>${subnets}</td><td>${isSelf ? `<span class="muted">自己</span>` : ""}</td></tr>`;
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
  const hasOwner = (status?.networks ?? []).some((n) => n.owner);
  const srv = currentServer();
  const hint = hasOwner
    ? `<p class="msg">本设备已创建网络（每客户端仅能创建一个）。如需新网络，请先删除或退出当前网络。</p>`
    : "";
  const serverHint = `<p class="hint">SnetServ${srv ? `: ${esc(srv)}` : ""}</p>`;
  openModal({
    title: "创建网络",
    body: `${hint}
      <div class="row"><label>网络名称</label><input id="m-name" type="text" placeholder="例如：家庭网络" /></div>
      <div class="row"><label>网段</label>${subnetWidgetHTML({ id: "m-subnet", placeholder: "留空自动分配（如 10.88.0.0/24）", emptyHint: "留空自动分配，通常为 10.88.N.0/24" })}</div>
      ${serverHint}
      <p class="msg" id="m-result"></p>`,
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
          const r: CreateResp = await backend.create({ server, port: s.wgport, name, subnet, approvalRequired: false });
          const modalEl = body;
          const mBody = modalEl.querySelector<HTMLElement>(".modal-body")!;
          const mFoot = modalEl.querySelector<HTMLElement>(".modal-foot")!;
          mBody.innerHTML = `
            <div class="create-ok">
              <p class="msg ok">网络创建成功，邀请其他设备加入：</p>
              <div class="qr" id="qr"></div>
              <div class="kv"><span>网络ID</span><code>${esc(r.networkId)}</code></div>
              <div class="kv"><span>配对码</span><code>${esc(r.pairingCode)}</code></div>
              <div class="kv"><span>邀请链接</span><code>${esc(r.link)}</code></div>
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
    body: `<div class="row"><label>邀请链接</label>
      <div class="subnet-input join-link-row">
        <input class="join-link-input" id="m-link" type="text" placeholder="snet://join?nid=...&code=..." />
        ${backend.scanQR ? '<button id="m-scan" class="btn ghost" type="button" title="扫描二维码加入">扫码</button>' : ""}
      </div></div>
      <p class="hint" style="text-align:center">或手动输入</p>
      <div class="row"><label>网络ID</label><input id="m-nid" type="text" placeholder="6 位网络ID" /></div>
      <div class="row"><label>配对码</label><input id="m-code" type="text" placeholder="12 位配对码" /></div>
      <p class="msg" id="m-result"></p>`,
    footer: `<button data-close class="btn ghost">取消</button><button id="m-submit" class="btn">加入</button>`,
    onBody: (body) => {
      const submit = body.querySelector<HTMLButtonElement>("#m-submit")!;
      const result = body.querySelector<HTMLElement>("#m-result")!;
      const linkInput = body.querySelector<HTMLInputElement>("#m-link")!;
      const scanBtn = body.querySelector<HTMLButtonElement>("#m-scan");
      scanBtn?.addEventListener("click", async () => {
        if (!backend.scanQR) return;
        scanBtn.disabled = true;
        scanBtn.textContent = "扫描中…";
        try {
          const text = (await backend.scanQR()).trim();
          if (!/^snet:\/\/join\?/.test(text)) {
            toast("不是有效的加入邀请链接", "err");
            return;
          }
          linkInput.value = text;
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
          let link = linkInput.value.trim();
          if (!link) {
            const nid = (body.querySelector("#m-nid") as HTMLInputElement).value.trim();
            const code = (body.querySelector("#m-code") as HTMLInputElement).value.trim();
            if (!nid || !code) throw new Error("请输入邀请链接，或网络ID + 配对码");
            link = `snet://join?nid=${nid}&code=${code}`;
          }
          const server = linkServerOf(link) || currentServer();
          const r: JoinResp = await backend.join({ server, port: s.wgport, link });
          result.className = "msg ok";
          if (r.status === "pending") {
            result.innerHTML = `<p>已提交加入请求，等待网络创建者批准。</p><p class="hint">批准后本机会自动加入并连接；也可在上方「待批准 · 加入请求」卡片中取消。</p>`;
          } else {
            result.textContent = `已加入: IP ${r.ip ?? "-"}，网络 ${r.networkId ?? "-"}`;
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
          submit.disabled = false;
          submit.innerHTML = "加入";
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
  const statusSource = document.getElementById("status-source");
  const statusGridHTML = statusSource?.querySelector(".status-grid")?.innerHTML || "";
  const statusJSONHTML = statusSource?.querySelector(".card")?.outerHTML || "";
  const server = (status?.serverAddr || s.server || "").trim();
  const isOfficial = !server || server.replace(/\/+$/, "") === DEFAULT_SERVER.replace(/\/+$/, "");
  const nat = status?.natMapping;
  const natEnabled = nat?.enabled !== false;
  const natLines = (nat?.mappings ?? []).map(m =>
    `UDP ${m.port} → ${m.externalIp}:${m.externalPort} · ${natViaLabel(m.via)}`
  );
  const natErr = nat?.errors?.[0];
  openModal({
    title: "信息",
    body: `<div class="info-status">
        <div class="detail-section-head"><h3>运行状态</h3></div>
        <div class="status-grid info-status-grid">
          ${statusGridHTML}
          <div class="stat coordinator-card" id="s-server-card">
            <div class="label">协调服务</div>
            <div id="s-server-view">
              <div class="value" id="s-server-label">${isOfficial ? "官方 (SnetServ)" : esc(server)}</div>
              <div class="coordinator-actions">
                <button type="button" id="s-server-edit" class="btn icon sm" title="修改">✎</button>
                ${isOfficial ? "" : '<button type="button" id="s-server-reset" class="btn ghost sm">恢复默认</button>'}
              </div>
            </div>
            <div id="s-server-editor" hidden>
              <input id="s-server" type="url" value="${isOfficial ? "" : esc(server)}" placeholder="https://my-vps:8090" autocomplete="off" />
              <p class="hint">留空则使用官方 SnetServ，或填入自托管地址。</p>
              <div class="settings-actions"><button id="s-server-save" class="btn">保存</button><button id="s-server-cancel" class="btn ghost">取消</button></div>
            </div>
            <p class="msg" id="s-server-result"></p>
          </div>
        </div>
      </div>
      <div class="row"><label>WireGuard 端口</label><input id="s-wgport" type="number" min="1024" max="65535" value="${s.wgport}" /></div>
      <div class="settings-block">
        <div class="row" style="display:flex;align-items:center;justify-content:space-between;gap:12px;margin-bottom:6px">
          <div>
            <label style="margin-bottom:2px">自动端口映射</label>
            <div class="hint">通过 UPnP / NAT-PMP / PCP 让对端直连，默认开启</div>
          </div>
          <label class="switch">
            <input id="s-nat" type="checkbox" ${natEnabled ? "checked" : ""} ${!status ? "disabled" : ""} />
            <span class="slider"></span>
          </label>
        </div>
        <p class="msg" id="s-nat-state">${!status ? "后台服务未运行" : !natEnabled ? "已关闭" : natLines.length ? `已映射 ${natLines.length} 个端口` : "正在等待路由器分配映射…"}</p>
        <div class="hint" id="s-nat-list">${natLines.map(esc).join("<br>")}${natErr ? `<div style="color:var(--err)">端口 ${natErr.port}: ${esc(natErr.error)}</div>` : ""}</div>
      </div>
      <div class="settings-actions">
        <button id="s-logs" class="btn ghost" type="button">导出诊断日志</button>
      </div>
      <div class="settings-actions" id="s-daemon-row">
        ${backend.hasDaemonControl
          ? (status ? `<span class="live-dot ok">运行中</span>` : `<button id="s-start-daemon" class="btn ghost">启动</button>`)
          : ""}
      </div>
      <p class="msg" id="s-msg"></p>
      <div class="info-json">${statusJSONHTML}</div>
    `,
    wide: true,
    footer: `<button data-close class="btn ghost">关闭</button>`,
    onBody: (body) => {
      const msg = body.querySelector<HTMLElement>("#s-msg")!;
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

      const natInput = body.querySelector<HTMLInputElement>("#s-nat");
      const natState = body.querySelector<HTMLElement>("#s-nat-state");
      natInput?.addEventListener("change", async () => {
        if (!natState) return;
        const enabled = natInput.checked;
        natInput.disabled = true;
        natState.className = "msg";
        natState.textContent = enabled ? "正在开启…" : "正在关闭…";
        try {
          await backend.setNATMapping(enabled);
          await refresh();
          natState.className = "msg ok";
          natState.textContent = enabled ? "已开启自动端口映射" : "已关闭自动端口映射";
          toast(natState.textContent);
        } catch (e) {
          natInput.checked = !enabled;
          natState.className = "msg";
          natState.textContent = `保存失败: ${e}`;
        } finally {
          natInput.disabled = false;
        }
      });

      body.querySelector("#s-logs")?.addEventListener("click", async (e) => {
        const btn = e.currentTarget as HTMLButtonElement;
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
  const subnets = (n.allowedSubnets?.length ? n.allowedSubnets : self?.allowedSubnets) ?? [];
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
  const subnetHTML = subnets.length
    ? subnets.map((s) => `<span class="path-subnet">${esc(s)}</span>`).join("")
    : `<span class="muted">未宣告子网</span>`;
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
        <div class="path-subnets"><span class="path-label">宣告子网</span>${subnetHTML}</div>
      </div>
      ${p2p}
      <div class="path-peers">${peerHTML}</div>
    </div>`,
    direct,
    relay,
    probing,
  };
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

function memberTableHTML(n: NetInfo, r: PeersResp | undefined, canManage: boolean, selfRole: string): string {
  if (!r) return `<p class="hint">${peerErrors[n.networkId] ? "该网络已失效或加载失败" : "正在获取成员信息…"}</p>`;
  const self = r.self;
  const list: NetNode[] = self ? [self, ...(r.peers ?? [])] : (r.peers ?? []);
  if (!list.length) return `<p class="hint">暂无成员信息</p>`;
  const rows = list.map((nd) => {
    const isSelf = nd.ip === n.ip || (!!self && nd.id === self.id);
    const role = String(isSelf ? selfRole : (nd.role || "member")).trim().toLowerCase();
    const online = nd.online
      ? '<span class="pill ok">在线</span>'
      : '<span class="pill off">离线</span>';
    const subnets = (nd.allowedSubnets?.length ?? 0) > 0
      ? `<span class="pill subnet-route" title="子网路由：${esc(nd.allowedSubnets!.join(", "))}">${esc(nd.allowedSubnets!.join(", "))}</span>`
      : '<span class="muted">-</span>';
    const rolePill = role === "owner"
      ? `<span class="pill accent">${roleLabel(role)}</span>`
      : `<span class="pill off">${roleLabel(role)}</span>`;
    const diag = isSelf
      ? '<td><span class="muted">-</span></td>'
      : `<td class="ping-cell"><button type="button" class="btn ghost sm" data-detail-ping="${esc(nd.id)}" data-ping-target="${esc(nd.ip || "")}">📡 诊断</button><span class="ping-result" data-ping-result="${esc(nd.id)}"></span></td>`;
    const op = canManage
      ? `<td>${isSelf ? '<span class="muted">自己</span>' : `<button type="button" class="btn danger ghost sm" data-detail-kick="${esc(nd.id)}">移除</button>`}</td>`
      : "";
    return `<tr>
      <td>${deviceLabel(nd)}</td>
      <td class="mono">${esc(nd.ip || "-")}</td>
      <td>${online}</td>
      <td>${subnets}</td>
      <td>${rolePill}</td>
      ${diag}
      ${op}
    </tr>`;
  }).join("");
  return `<div class="tbl-wrap"><table class="members"><thead><tr>
    <th>设备名 / 设备ID</th><th>虚拟 IP</th><th>状态</th><th>子网路由</th><th>角色</th><th>诊断</th>${canManage ? "<th>操作</th>" : ""}
  </tr></thead><tbody>${rows}</tbody></table></div>`;
}

async function runPeerPing(btn: HTMLButtonElement) {
  const target = btn.dataset.pingTarget || "";
  const result = btn.closest("td")?.querySelector<HTMLElement>("[data-ping-result]");
  if (!target) {
    if (result) { result.className = "ping-result err"; result.textContent = "无 IP"; }
    return;
  }
  const original = btn.textContent || "📡 诊断";
  btn.disabled = true;
  btn.textContent = "诊断中…";
  if (result) { result.className = "ping-result"; result.textContent = ""; }
  try {
    const r = await backend.ping(target);
    const loss = Math.max(0, Number(r.loss) || 0);
    const avg = Math.max(0, Number(r.avgRtt) || 0);
    const cls = loss >= 50 || avg >= 200 ? "err" : loss > 0 || avg >= 80 ? "warn" : "ok";
    if (result) {
      result.className = "ping-result " + cls;
      result.textContent = `${loss.toFixed(0)}% 丢包 / ${avg > 0 ? `${avg.toFixed(0)}ms` : "超时"}`;
    }
  } catch (e) {
    if (result) {
      result.className = "ping-result err";
      result.textContent = "诊断失败";
    }
    toast(String(e), "err");
  } finally {
    btn.disabled = false;
    btn.textContent = original;
  }
}

async function inviteInfo(nid: string): Promise<{ link: string; code: string } | null> {
  let detail = ownerInfo[nid];
  if (!detail) {
    try {
      detail = await backend.netinfo(nid);
      ownerInfo[nid] = detail;
    } catch (e) {
      toast(`获取邀请信息失败: ${e}`, "err");
      return null;
    }
  }
  let code = detail.pairingCode || "";
  if (!code) {
    const nr = await ctlOrClean(nid, "邀请加入", () => backend.resetCode(nid));
    if (nr === null) return null;
    code = nr.pairingCode;
    detail.pairingCode = code;
    ownerInfo[nid] = detail;
    toast("原配对码不可用，已生成新码（旧码作废）", "warn");
  }
  const netId = detail.id || detail.networkId || nid;
  const name = detail.name || "";
  let link = `snet://join?nid=${encodeURIComponent(netId)}&code=${encodeURIComponent(code)}`;
  if (name) link += `&name=${encodeURIComponent(name)}`;
  return { link, code };
}

async function copyInviteLink(nid: string, btn: HTMLButtonElement) {
  btn.disabled = true;
  try {
    const info = await inviteInfo(nid);
    if (info) await copyText(info.link, btn);
  } finally {
    btn.disabled = false;
  }
}

async function copyPairingCode(nid: string, btn: HTMLButtonElement) {
  btn.disabled = true;
  try {
    const info = await inviteInfo(nid);
    if (info) await copyText(info.code, btn);
  } finally {
    btn.disabled = false;
  }
}

async function leaveNetworkFromDetail(nid: string) {
  if (!(await confirmDialog("退出网络", "退出后将断开该网络，确定继续？", true))) return;
  try {
    const r = await ctlOrClean(nid, "退出网络", () => backend.leave(nid));
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
    openModal({
      title: `网络详情 · ${nid}`,
      wide: true,
      body: `<pre class="json">${esc(JSON.stringify(r, null, 2))}</pre>`,
      footer: `<button data-close class="btn ghost">关闭</button>`,
    });
  } catch (e) {
    toast(`获取网络详情失败: ${e}`, "err");
  }
}

async function resetPairingCode(nid: string) {
  if (!(await confirmDialog("重置配对码", "作废旧配对码并生成新码？旧码立即失效，已加入成员不受影响。", true))) return;
  try {
    const r = await ctlOrClean(nid, "重置配对码", () => backend.resetCode(nid));
    if (r === null) return;
    toast("已生成新配对码");
    await refresh();
  } catch (e) {
    toast(String(e), "err");
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

function networkDetailHTML(n: NetInfo): string {
  const r = peerDetails[n.networkId];
  const role = networkRole(n);
  const canManage = canManageNetwork(n);
  const path = pathMonitorHTML(n, r, false);
  const members = r ? `${(r.peers ?? []).length + 1} 位成员` : "加载中";
  const more = `<div class="detail-more">
    <button type="button" class="btn ghost sm" id="detail-more-btn">更多</button>
    <div class="detail-more-menu" id="detail-more-menu" hidden>
      <button type="button" data-detail-act="detail">网络详情</button>
      ${canManage ? '<button type="button" data-detail-act="settings">编辑网络</button>' : ""}
      ${canManage ? '<button type="button" data-detail-act="subnets">子网路由管理</button>' : ""}
      ${canManage ? '<button type="button" data-detail-act="reset-code">重置配对码</button>' : ""}
      ${canManage ? "" : '<button type="button" data-detail-act="leave">退出网络</button>'}
      ${canManage ? '<button type="button" class="danger" data-detail-act="delete">删除网络</button>' : ""}
    </div>
  </div>`;
  return `<div class="net-detail-head">
    <div class="detail-heading">
      <div class="detail-title">${esc(n.name || n.networkId)}</div>
      <div class="detail-sub">${esc(n.networkId)} · ${roleLabel(role)} · ↑ ${fmtSpeed(status?.txSpeed)} ↓ ${fmtSpeed(status?.rxSpeed)}</div>
    </div>
    <label class="switch detail-conn-switch" title="连接 / 断开">
      <input id="detail-net-toggle" type="checkbox" ${n.active ? "checked" : ""} ${n.serverState === "gone" ? "disabled" : ""} />
      <span class="slider"></span>
    </label>
    ${canManage ? '<button type="button" class="btn ghost sm" id="detail-invite" data-detail-act="invite">邀请</button>' : ""}
    ${more}
  </div>
  <section class="card detail-section">
    <div class="detail-section-head">
      <h3>组网路径与拓扑</h3>
      <span class="hint">${path.direct} 直连 · ${path.relay} 中继${path.probing ? ` · ${path.probing} 打洞中` : ""}</span>
    </div>
    ${path.html}
  </section>
  <section class="card detail-section">
    <div class="detail-section-head"><h3>成员列表</h3><span class="hint">${members}</span></div>
    ${memberTableHTML(n, r, canManage, role)}
  </section>`;
}

function pendingDetailHTML(p: NonNullable<DaemonStatus["pendingJoins"]>[number]): string {
  return `<div class="net-detail-head">
    <div class="detail-heading">
      <div class="detail-title">${esc(p.networkId)}</div>
      <div class="detail-sub">加入请求 · 等待批准</div>
    </div>
    <div class="detail-more">
      <button type="button" class="btn ghost sm" id="detail-more-btn">更多</button>
      <div class="detail-more-menu" id="detail-more-menu" hidden>
        <button type="button" data-detail-act="cancel-pending">取消加入请求</button>
      </div>
    </div>
  </div>
  <section class="card detail-section">
    <div class="detail-section-head"><h3>等待网络创建者批准</h3></div>
    <p class="muted">请求已提交。网络创建者批准后，该网络会自动出现在上方 Tabs 中并建立连接。</p>
    <div class="detail-actions">
      <button type="button" class="btn danger ghost sm" data-detail-act="cancel-pending">取消加入请求</button>
    </div>
  </section>`;
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
    peerDetails[nid] = await withTimeout(backend.peers(nid), 5000, "成员信息加载超时");
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
    const pingEl = target.closest<HTMLButtonElement>("[data-detail-ping]");
    if (pingEl && detailNid) {
      await runPeerPing(pingEl);
      return;
    }
    const el = (e.target as HTMLElement).closest<HTMLElement>("[data-detail-act], [data-detail-kick]");
    if (!el || !detailNid) return;
    const nid = detailNid;
    if (el.dataset.detailKick) {
      if (!(await confirmDialog("移除成员", "移除该成员？该设备将立即断开。", true))) return;
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
    switch (el.dataset.detailAct) {
      case "detail": await openNetworkDetailModal(nid); break;
      case "invite": await openInviteModal(nid); break;
      case "settings": await openNetworkSettings(nid); break;
      case "subnets": await openSubnetRouteModal(nid); break;
      case "reset-code": await resetPairingCode(nid); break;
      case "leave": await leaveNetworkFromDetail(nid); break;
      case "delete": await deleteNetworkFromDetail(nid, el as HTMLButtonElement); break;
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
  initTabs();
  bindNetListEvents();
  bindNetworkDetailEvents();
  bindEvents();
  // Hide logout button on Desktop/Android (not needed without login)
  if (backend.hasDaemonControl) {
    const lo = $("#btn-logout") as HTMLElement | null;
    if (lo) lo.style.display = "none";
    document.querySelectorAll<HTMLElement>("[data-action=\"logout\"]").forEach((el) => { el.style.display = "none"; });
  }
  refresh();
  setInterval(() => {
    if (!document.hidden && !refreshing) refresh();
  }, 5000);
}
