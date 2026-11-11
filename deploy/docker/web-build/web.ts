/* ── Web adapter: fetch HTTP → shared UI ────────────────────────
 * Thin adapter: provides Backend + auth flow, delegates UI to shared/ui.ts.
 */
import type { Backend, DaemonStatus, NetInfoDetail, PeersResp, CreateResp, JoinResp } from "../../../shared/web/types.js";
import { FIXED_SERVER } from "../../../shared/web/types.js";
import { init } from "../../../shared/web/ui.js";

const NS = "/ctl";
const AUTH_NS = "/ctl/auth";
let authToken: string | null = localStorage.getItem("snet_token") || null;

/* ── fetch helpers ────────────────────────────────────────────── */
async function api(path: string, opts?: RequestInit): Promise<any> {
  const headers: Record<string, string> = { ...((opts?.headers as Record<string, string>) || {}) };
  if (authToken) headers["Authorization"] = "Bearer " + authToken;
  const res = await fetch(NS + path, { ...opts, headers });
  const text = await res.text();
  if (!res.ok) {
    if (res.status === 401) {
      authToken = null;
      localStorage.removeItem("snet_token");
      // Wails desktop must never fall back to the browser login/setup flow.
      if (!isWailsDesktop()) showPage("login-page");
    }
    let msg = "HTTP " + res.status;
    try { const j = JSON.parse(text); if (j.error) msg = j.error; } catch {}
    throw new Error(msg);
  }
  if (!text) return null;
  return JSON.parse(text);
}

async function authApi(path: string, opts?: RequestInit): Promise<any> {
  const headers: Record<string, string> = (opts?.headers as Record<string, string>) || {};
  if (authToken) headers["Authorization"] = "Bearer " + authToken;
  if (opts?.body && !headers["Content-Type"]) headers["Content-Type"] = "application/json";
  const res = await fetch(AUTH_NS + path, { ...opts, headers });
  const text = await res.text();
  if (!res.ok) {
    let msg = "HTTP " + res.status;
    try { const j = JSON.parse(text); if (j.error) msg = j.error; } catch {}
    throw new Error(msg);
  }
  if (!text) return null;
  return JSON.parse(text);
}

function jsonPost(path: string, body: Record<string, unknown>): RequestInit {
  return { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) };
}

/* ── Auth flow ────────────────────────────────────────────────── */
async function checkAuth(): Promise<"no_password" | "needs_login" | "authenticated"> {
  try {
    const resp = await authApi("/check");
    if (!resp.hasPassword) return "no_password";
    if (resp.authenticated) return "authenticated";
    return "needs_login";
  } catch {
    return "needs_login";
  }
}

async function doLogin(password: string, remember: boolean): Promise<void> {
  const resp = await authApi("/login", { method: "POST", body: JSON.stringify({ password, remember }) });
  authToken = resp.token;
  localStorage.setItem("snet_token", authToken!);
}

async function doLogout(): Promise<void> {
  try { await authApi("/logout", { method: "POST" }); } catch {}
  authToken = null;
  localStorage.removeItem("snet_token");
}

async function doSetPassword(current: string, newPass: string): Promise<void> {
  await authApi("/password", { method: "POST", body: JSON.stringify({ current, new: newPass }) });
}

/* ── Wails detection ──────────────────────────────────────────── */
function isWailsDesktop(): boolean {
  const loc = typeof window !== "undefined" ? window.location : null;
  return typeof (window as any).runtime !== "undefined" ||
    typeof (window as any)._wails !== "undefined" ||
    typeof (window as any).wails !== "undefined" ||
    typeof (window as any).go !== "undefined" ||
    (!!loc && (loc.protocol === "wails:" || loc.hostname === "wails.localhost"));
}

/* ── Page routing ─────────────────────────────────────────────── */
function showPage(id: string) {
  for (const p of ["app", "login-page", "setup-page", "onboarding-page"]) {
    const el = document.getElementById(p);
    if (el) el.hidden = (p !== id);
  }
}

/* ── Backend implementation ───────────────────────────────────── */
const backend: Backend = {
  hasDaemonControl: false,

  async status(): Promise<DaemonStatus | null> {
    try { return await api("/status"); } catch { return null; }
  },

  async create(params: { server: string; port: number; name: string; subnet: string; approvalRequired: boolean; description?: string; tags?: string[]; visibility?: string }): Promise<CreateResp> {
    const r = await api("/create", jsonPost("/create", {
      name: params.name, subnet: params.subnet, port: params.port,
      approvalRequired: params.approvalRequired,
      description: params.description ?? "", tags: params.tags ?? [], visibility: params.visibility ?? "",
    }));
    const link = `snet://join?nid=${encodeURIComponent(r.networkId)}&code=${encodeURIComponent(r.pairingCode)}` +
      (params.server ? `&server=${encodeURIComponent(params.server)}` : "");
    return { ...r, link };
  },

  async join(params: { server: string; port: number; link: string }): Promise<JoinResp> {
    return api("/join", jsonPost("/join", {
      link: params.link, port: params.port,
    }));
  },

  async bind(params: { server: string; code: string }): Promise<void> {
    await api("/bind", jsonPost("/bind", { server: params.server, code: params.code }));
  },

  async rejoin(nid: string): Promise<void> { await api("/rejoin", jsonPost("/rejoin", { nid })); },
  async leave(nid: string): Promise<void> { await api("/leave", jsonPost("/leave", { nid })); },
  async remove(nid: string): Promise<void> { await api("/remove", jsonPost("/remove", { nid })); },
  async deleteNet(nid: string): Promise<void> { await api("/delete", jsonPost("/delete", { nid })); },

  async netinfo(nid: string): Promise<NetInfoDetail> { return api("/netinfo?nid=" + encodeURIComponent(nid)); },
  async peers(nid: string): Promise<PeersResp> { return api("/peers?nid=" + encodeURIComponent(nid)); },

  async updateSettings(params: { nid: string; name: string; subnet: string; approvalRequired: boolean | null }): Promise<void> {
    await api("/settings", jsonPost("/settings", {
      nid: params.nid, name: params.name, subnet: params.subnet, approvalRequired: params.approvalRequired,
    }));
  },

  async setNATMapping(enabled: boolean): Promise<void> {
    await api("/nat", jsonPost("/nat", { enabled }));
  },

  async setRole(params: { nid: string; nodeId: string; role: string }): Promise<void> {
    await api("/role", jsonPost("/role", { nid: params.nid, nodeId: params.nodeId, role: params.role }));
  },

  async updateSubnets(params: { nid: string; subnets: string[] }): Promise<void> {
    await api("/subnets", jsonPost("/subnets", { nid: params.nid, subnets: params.subnets }));
  },

  async kick(params: { nid: string; nodeId: string }): Promise<void> {
    await api("/kick", jsonPost("/kick", { nid: params.nid, nodeId: params.nodeId }));
  },

  async approve(params: { nid: string; pendingId: string }): Promise<void> {
    await api("/approve", jsonPost("/approve", { nid: params.nid, pendingId: params.pendingId }));
  },

  async deny(params: { nid: string; pendingId: string }): Promise<void> {
    await api("/deny", jsonPost("/deny", { nid: params.nid, pendingId: params.pendingId }));
  },

  async cancelPending(pendingId: string): Promise<void> {
    await api("/cancel-pending", jsonPost("/cancel-pending", { pendingId }));
  },

  async resetCode(nid: string): Promise<{ pairingCode: string }> {
    return api("/reset-code", jsonPost("/reset-code", { nid }));
  },

  async detectLocalSubnets(): Promise<string[]> {
    const resp = await api("/local-subnets");
    return resp?.subnets ?? (Array.isArray(resp) ? resp : []);
  },

  async ensureDaemon(): Promise<void> {
    if (!isWailsDesktop()) {
      throw new Error("Web 版无法管理后台服务，请手动启动 snetd");
    }
    const res = await fetch("/desktop/start", { method: "POST" });
    if (!res.ok) {
      const text = await res.text();
      throw new Error(text || ("启动服务失败: HTTP " + res.status));
    }
  },
};

/* ── Bootstrap ────────────────────────────────────────────────── */
function startApp() {
  showPage("app");
  if (isWailsDesktop()) {
    document.documentElement.classList.add("wails-desktop");
    backend.hasDaemonControl = true;
    // The desktop app is a trusted local process and its Go ctl proxy already
    // injects the ctl-token. Do not expose the browser login/logout flow, and
    // ignore stale browser-era state from a previous Web/Docker deployment.
    authToken = null;
    try {
      for (let i = localStorage.length - 1; i >= 0; i--) {
        const key = localStorage.key(i);
        if (key && (key === "snet_token" || key.startsWith("snet."))) {
          localStorage.removeItem(key);
        }
      }
    } catch { /* localStorage may be unavailable in some webviews */ }
    const logout = document.getElementById("btn-logout");
    if (logout) logout.style.display = "none";
    document.querySelectorAll<HTMLElement>('[data-action="logout"]').forEach((el) => {
      el.style.display = "none";
    });
  }
  init(backend);
}

// After login/setup, an unbound device must be guided to bind before it can
// reach the console. Skipping the onboarding page is still allowed.
async function routeAfterAuth() {
  try {
    const st = await backend.status();
    if (st?.bound) { startApp(); return; }
  } catch { /* daemon unreachable: fall through to onboarding */ }
  showPage("onboarding-page");
}

document.addEventListener("DOMContentLoaded", () => {
  // Login form
  const loginSubmit = document.getElementById("login-submit");
  if (loginSubmit) loginSubmit.addEventListener("click", async () => {
    const pw = (document.getElementById("login-password") as HTMLInputElement)?.value;
    const remember = (document.getElementById("login-remember") as HTMLInputElement)?.checked ?? true;
    const result = document.getElementById("login-result")!;
    if (!pw) { result.className = "msg"; result.textContent = "请输入密码"; return; }
    (loginSubmit as HTMLButtonElement).disabled = true;
    result.className = "msg"; result.textContent = "正在登录…";
    try {
      await doLogin(pw, remember);
      await routeAfterAuth();
    } catch (e) {
      result.className = "msg"; result.textContent = "登录失败: " + e;
      (loginSubmit as HTMLButtonElement).disabled = false;
    }
  });
  document.getElementById("login-password")?.addEventListener("keydown", (e) => { if (e.key === "Enter") loginSubmit?.click(); });

  // Setup form
  const setupSubmit = document.getElementById("setup-submit");
  if (setupSubmit) setupSubmit.addEventListener("click", async () => {
    const pw = (document.getElementById("setup-password") as HTMLInputElement)?.value;
    const pw2 = (document.getElementById("setup-password2") as HTMLInputElement)?.value;
    const result = document.getElementById("setup-result")!;
    if (!pw || pw.length < 8) { result.className = "msg"; result.textContent = "密码至少需要 8 个字符"; return; }
    if (pw !== pw2) { result.className = "msg"; result.textContent = "两次输入不一致"; return; }
    (setupSubmit as HTMLButtonElement).disabled = true;
    result.className = "msg"; result.textContent = "正在设置…";
    try {
      await doSetPassword("", pw);
      await doLogin(pw, true);
      await routeAfterAuth();
    } catch (e) {
      result.className = "msg"; result.textContent = "设置失败: " + e;
      (setupSubmit as HTMLButtonElement).disabled = false;
    }
  });
  document.getElementById("setup-password2")?.addEventListener("keydown", (e) => { if (e.key === "Enter") setupSubmit?.click(); });

  // Onboarding bind
  const onbBind = document.getElementById("onb-bind");
  if (onbBind) onbBind.addEventListener("click", async () => {
    const code = (document.getElementById("onb-code") as HTMLInputElement)?.value.trim();
    const result = document.getElementById("onb-bind-result")!;
    if (!code) { result.className = "msg"; result.textContent = "请输入授权码"; return; }
    result.className = "msg"; result.textContent = "正在绑定…";
    try {
      await backend.bind({ server: FIXED_SERVER, code });
      result.className = "msg ok"; result.textContent = "已绑定设备授权";
      startApp();
    } catch (e) { result.className = "msg"; result.textContent = "绑定失败: " + e; }
  });

  const onbSkip = document.getElementById("onb-skip");
  if (onbSkip) onbSkip.addEventListener("click", (e) => {
    e.preventDefault();
    startApp();
  });

  // Password visibility toggles
  document.querySelectorAll(".toggle-pw").forEach((btn) => {
    btn.addEventListener("click", () => {
      const input = document.getElementById((btn as HTMLElement).dataset.target!);
      if (!input) return;
      const show = (input as HTMLInputElement).type === "password";
      (input as HTMLInputElement).type = show ? "text" : "password";
      btn.innerHTML = show
        ? '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="16" height="16"><path d="M17.94 17.94A10.07 10.07 0 0112 20c-7 0-11-8-11-8a18.45 18.45 0 015.06-5.94M9.9 4.24A9.12 9.12 0 0112 4c7 0 11 8 11 8a18.5 18.5 0 01-2.16 3.19m-6.72-1.07a3 3 0 11-4.24-4.24"/><line x1="1" y1="1" x2="23" y2="23"/></svg>'
        : '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="16" height="16"><path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"/><circle cx="12" cy="12" r="3"/></svg>';
    });
  });

  // Logout (listen for shared UI event)
  window.addEventListener("snet-logout", async () => {
    await doLogout();
    showPage("login-page");
  });
  document.getElementById("btn-logout")?.addEventListener("click", async () => {
    await doLogout();
    showPage("login-page");
  });

  // Initial auth check. Wails desktop is a trusted local process and must not
  // show the browser setup/login pages.
  if (isWailsDesktop()) {
    startApp();
    return;
  }
  (async () => {
    const authState = await checkAuth();
    if (authState === "no_password") { showPage("setup-page"); return; }
    if (authState === "needs_login") { showPage("login-page"); return; }
    await routeAfterAuth();
  })();
});
