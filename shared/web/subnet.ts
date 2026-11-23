/* ── Shared subnet validation widget ──────────────────────────────
 * Rules mirror server-side internal/server/store.go validateSubnet:
 * IPv4 private ranges only (10/8, 172.16/12, 192.168/16),
 * prefix /8–/24, host bits must be zero.
 * Keep in sync with internal/server/admin.html and internal/server/store.go.
 */
import { esc } from "./utils.js";

export type SubnetCheck =
  | { ok: true; value: string; fix?: boolean; text: string }
  | { ok: false; error: string };

export const SUBNET_PREFIXES = [24, 23, 22, 20, 16, 12, 8];
export const SUBNET_CHIPS = ["10.88.0.0/24", "10.0.0.0/8", "172.16.0.0/12", "192.168.1.0/24"];

/* Private-range rules: the first octet picks the family and the last control
 * offers only prefixes that are valid for that family. */
const FIRST_OCTETS = [10, 172, 192];
const PREFIX_BY_FIRST: Record<number, number[]> = {
  10: [8, 16, 24],
  172: [12, 16, 24],
  192: [16, 24],
};

function defaultSecond(first: number): number {
  if (first === 172) return 16;
  if (first === 192) return 168;
  return 88;
}

function clampSecond(first: number, second: number): number {
  if (first === 172) return Math.min(31, Math.max(16, second));
  if (first === 192) return 168;
  return Math.min(255, Math.max(0, second));
}

function clampOctet(n: number): number {
  return Math.min(255, Math.max(0, Number.isFinite(n) ? Math.trunc(n) : 0));
}

function subnetIsPrivate(u32: number): boolean {
  if ((u32 >>> 24) === 10) return true; // 10/8
  if (u32 >= 0xac100000 && u32 <= 0xac1fffff) return true; // 172.16/12
  if (u32 >= 0xc0a80000 && u32 <= 0xc0a8ffff) return true; // 192.168/16
  return false;
}

function subnetBaseU32(u32: number, prefix: number): number {
  const mask = (~0 << (32 - prefix)) >>> 0;
  return (u32 & mask) >>> 0;
}

function u32ToIp(u32: number): string {
  return `${u32 >>> 24}.${(u32 >>> 16) & 255}.${(u32 >>> 8) & 255}.${u32 & 255}`;
}

export function subnetCheck(raw: string, selectPrefix: number): SubnetCheck {
  const s = raw.trim();
  if (!s) return { ok: true, value: "", text: "" };
  let host = s;
  let prefix = selectPrefix;
  const slash = s.indexOf("/");
  if (slash >= 0) {
    host = s.slice(0, slash);
    const p = Number(s.slice(slash + 1).trim());
    if (!Number.isInteger(p) || p < 8 || p > 24) {
      return { ok: false, error: "前缀需为 /8–/24" };
    }
    prefix = p;
  }
  const parts = host.split(".");
  if (parts.length !== 4) return { ok: false, error: "格式无效，如 10.88.0.0/24" };
  const oct = parts.map((o) => {
    if (!/^\d{1,3}$/.test(o)) return NaN;
    return Number(o);
  });
  if (oct.some((o) => Number.isNaN(o) || o < 0 || o > 255)) {
    return { ok: false, error: "IPv4 地址段需在 0–255" };
  }
  const u32 = ((oct[0] << 24) | (oct[1] << 16) | (oct[2] << 8) | oct[3]) >>> 0;
  const base = subnetBaseU32(u32, prefix);
  if (!subnetIsPrivate(base)) {
    return { ok: false, error: "仅支持私有内网段：10/8、172.16/12、192.168/16" };
  }
  const value = `${u32ToIp(base)}/${prefix}`;
  if (base !== u32) {
    return { ok: true, value, fix: true, text: `主机位应为 0，已修正为 ${value}` };
  }
  const capacity = Math.min(100, Math.max(0, 2 ** (32 - prefix) - 2)).toLocaleString("en-US");
  return { ok: true, value, text: `✓ ${value} · 私有网段，可容纳 ${capacity} 台` };
}

export type SubnetWidget = {
  check: () => SubnetCheck;
  get: () => string;
  render: () => void;
};

function parseValue(value: string): { first: number; second: number; third: number; fourth: number; prefix: number } {
  let first = 10, second = 88, third = 0, fourth = 0, prefix = 24;
  const slash = value.indexOf("/");
  const host = slash >= 0 ? value.slice(0, slash) : value;
  const p = Number(slash >= 0 ? value.slice(slash + 1) : "");
  if (Number.isInteger(p) && p >= 8 && p <= 24) prefix = p;
  const oct = host.split(".").map((v) => Number(v));
  if (oct.length === 4 && oct.every((v) => Number.isInteger(v) && v >= 0 && v <= 255)) {
    if (FIRST_OCTETS.includes(oct[0])) {
      first = oct[0];
      second = clampSecond(first, oct[1]);
      third = oct[2];
      fourth = oct[3];
    }
  }
  return { first, second, third, fourth, prefix };
}

function prefixOptions(first: number, current: number): number[] {
  const allowed = PREFIX_BY_FIRST[first] || [24];
  return allowed.includes(current) ? allowed : [...allowed, current].sort((a, b) => b - a);
}

export function subnetWidgetHTML(opts: { id?: string; value?: string; placeholder?: string; emptyHint?: string }): string {
  const v = parseValue(opts.value || "");
  const keep = opts.emptyHint || "";
  const firstOpts = FIRST_OCTETS.map((o) => `<option value="${o}" ${o === v.first ? "selected" : ""}>${o}</option>`).join("");
  const prefixOpts = prefixOptions(v.first, v.prefix)
    .map((p) => `<option value="${p}" ${p === v.prefix ? "selected" : ""}>/${p}</option>`)
    .join("");
  const chips = SUBNET_CHIPS.map((c) => `<button type="button" class="chip" data-subnet="${esc(c)}">${esc(c)}</button>`).join("");
  const keepRow = keep
    ? `<label class="subnet-keep"><input type="checkbox" class="subnet-keep-input" checked /> ${esc(keep)}</label>`
    : "";
  return `
    <div class="subnet-wrap" data-empty="${esc(keep)}">
      ${keepRow}
      <div class="subnet-input subnet-octets">
        <select class="subnet-oct subnet-oct1" aria-label="第一段（私有网段）">${firstOpts}</select>
        <span class="subnet-sep">.</span>
        <input class="subnet-oct subnet-oct2" type="number" min="0" max="255" value="${v.second}" aria-label="第二段" />
        <span class="subnet-sep">.</span>
        <input class="subnet-oct subnet-oct3" type="number" min="0" max="255" value="${v.third}" aria-label="第三段" />
        <span class="subnet-sep">.</span>
        <input class="subnet-oct subnet-oct4" type="number" min="0" max="255" value="${v.fourth}" aria-label="主机段" />
        <span class="subnet-sep">/</span>
        <select class="subnet-prefix" aria-label="前缀">${prefixOpts}</select>
      </div>
      <div class="subnet-chips">${chips}</div>
      <p class="subnet-status"></p>
    </div>`;
}

export function subnetWidgetInit(wrap: HTMLElement): SubnetWidget {
  const oct1 = wrap.querySelector<HTMLSelectElement>(".subnet-oct1")!;
  const oct2 = wrap.querySelector<HTMLInputElement>(".subnet-oct2")!;
  const oct3 = wrap.querySelector<HTMLInputElement>(".subnet-oct3")!;
  const oct4 = wrap.querySelector<HTMLInputElement>(".subnet-oct4")!;
  const prefixSel = wrap.querySelector<HTMLSelectElement>(".subnet-prefix")!;
  const status = wrap.querySelector<HTMLElement>(".subnet-status")!;
  const keepInput = wrap.querySelector<HTMLInputElement>(".subnet-keep-input");
  const emptyHint = wrap.dataset.empty || "";

  const base = (): string =>
    `${oct1.value}.${clampOctet(Number(oct2.value))}.${clampOctet(Number(oct3.value))}.${clampOctet(Number(oct4.value))}`;

  const syncSecond = () => {
    const first = Number(oct1.value);
    oct2.value = String(clampSecond(first, Number(oct2.value || defaultSecond(first))));
  };

  const syncPrefixOptions = () => {
    const first = Number(oct1.value);
    const cur = Number(prefixSel.value);
    const allowed = prefixOptions(first, cur);
    prefixSel.innerHTML = allowed
      .map((p) => `<option value="${p}" ${p === cur ? "selected" : ""}>/${p}</option>`)
      .join("");
  };

  const setEnabled = () => {
    const disabled = !!keepInput?.checked;
    [oct1, oct2, oct3, oct4, prefixSel].forEach((el) => {
      (el as HTMLInputElement | HTMLSelectElement).disabled = disabled;
    });
    wrap.querySelectorAll<HTMLButtonElement>(".subnet-chips .chip").forEach((c) => (c.disabled = disabled));
  };

  const render = () => {
    if (keepInput?.checked) {
      status.className = "subnet-status";
      status.textContent = emptyHint;
      return;
    }
    const chk = subnetCheck(base(), Number(prefixSel.value));
    if (!chk.ok) {
      status.className = "subnet-status err";
      status.textContent = chk.error;
      return;
    }
    status.className = chk.fix ? "subnet-status warn" : "subnet-status ok";
    status.textContent = chk.text;
  };

  oct1.addEventListener("change", () => {
    syncSecond();
    syncPrefixOptions();
    render();
  });
  [oct2, oct3, oct4].forEach((el) => el.addEventListener("input", () => {
    const n = clampOctet(Number(el.value));
    el.value = String(n);
    render();
  }));
  oct2.addEventListener("blur", () => {
    const first = Number(oct1.value);
    oct2.value = String(clampSecond(first, Number(oct2.value)));
    render();
  });
  prefixSel.addEventListener("change", render);
  keepInput?.addEventListener("change", () => {
    setEnabled();
    render();
  });

  wrap.querySelectorAll<HTMLButtonElement>(".subnet-chips .chip").forEach((c) => {
    c.addEventListener("click", () => {
      if (keepInput) keepInput.checked = false;
      const v = parseValue(c.dataset.subnet || "");
      oct1.value = String(v.first);
      oct2.value = String(v.second);
      oct3.value = String(v.third);
      oct4.value = String(v.fourth);
      syncPrefixOptions();
      prefixSel.value = String(v.prefix);
      setEnabled();
      render();
    });
  });

  syncSecond();
  syncPrefixOptions();
  setEnabled();
  render();
  return {
    check: () => {
      if (keepInput?.checked) return { ok: true, value: "", text: emptyHint };
      return subnetCheck(base(), Number(prefixSel.value));
    },
    get: () => {
      if (keepInput?.checked) return "";
      const c = subnetCheck(base(), Number(prefixSel.value));
      return c.ok ? c.value : "";
    },
    render,
  };
}
