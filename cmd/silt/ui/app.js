// Local UI auth (#89). The daemon mints a per-daemon bearer token and hands
// it to the operator's browser once, on the URL query. This captures it,
// tucks it into sessionStorage, scrubs it from the visible URL, and then
// attaches it to same-origin /api/ calls so state-changing requests are
// authorized. Reads work without it; cross-origin calls (the observatory
// hitting sibling daemons) never receive this daemon's token.
(function () {
  try {
    const here = new URL(window.location.href);
    const fromURL = here.searchParams.get("token");
    if (fromURL) {
      sessionStorage.setItem("siltToken", fromURL);
      here.searchParams.delete("token");
      history.replaceState(null, "", here.pathname + here.search + here.hash);
    }
  } catch (e) { /* non-fatal */ }

  const token = () => sessionStorage.getItem("siltToken") || "";
  const origFetch = window.fetch.bind(window);
  window.fetch = function (input, init) {
    init = init || {};
    try {
      const raw = input && input.url ? input.url : input;
      const url = new URL(raw, window.location.href);
      const sameOrigin = url.origin === window.location.origin;
      if (sameOrigin && url.pathname.indexOf("/api/") === 0 && token()) {
        const headers = new Headers(init.headers || (input && input.headers) || {});
        if (!headers.has("Authorization")) headers.set("Authorization", "Bearer " + token());
        init = Object.assign({}, init, { headers });
      }
    } catch (e) { /* leave the call untouched on any parse issue */ }
    return origFetch(input, init);
  };
})();
