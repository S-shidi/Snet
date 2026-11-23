(function () {
  "use strict";

  // Lets shared/web-select the trusted native shell instead of the browser
  // login/setup pages.
  window.__SNET_ANDROID__ = true;

  // The UI is served from https://appassets.androidplatform.net. Rewrite its
  // relative /ctl calls to the loopback proxy created by SnetCore.Start().
  // The proxy injects the ctl-token and CORS headers.
  const BASE = "http://127.0.0.1:8081";
  const originalFetch = window.fetch.bind(window);

  window.fetch = function (input, init) {
    try {
      if (typeof input === "string" && input.startsWith("/ctl")) {
        input = BASE + input;
      } else if (input instanceof Request) {
        const url = new URL(input.url);
        if (url.pathname.startsWith("/ctl")) {
          input = new Request(BASE + url.pathname + url.search, input);
        }
      }
    } catch (_) {
      // Leave malformed inputs to the original fetch implementation.
    }
    return originalFetch(input, init);
  };

  // QR scan bridge used by the join modal. MainActivity calls these globals
  // after the ZXing scanner returns.
  window.__snetScanResult = function (text) {
    const resolve = window.__snetScanResolve;
    window.__snetScanResolve = null;
    window.__snetScanReject = null;
    if (resolve) resolve(text);
  };
  window.__snetScanError = function (message) {
    const reject = window.__snetScanReject;
    window.__snetScanResolve = null;
    window.__snetScanReject = null;
    if (reject) reject(new Error(message || "扫码已取消"));
  };

  // APK download & install progress bridge called from MainActivity.
  window.__snetUpgradeState = { state: "idle", percent: 0, downloaded: 0, total: 0, message: "" };
  window.__snetUpgradeProgress = function (payload) {
    if (!payload || typeof payload !== "object") return;
    window.__snetUpgradeState = payload;
    if (typeof window.__snetOnUpgradeProgress === "function") {
      try { window.__snetOnUpgradeProgress(payload); } catch (_) {}
    }
  };

})();
