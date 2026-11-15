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
})();
