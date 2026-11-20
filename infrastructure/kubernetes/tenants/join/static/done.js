/* Where Keycloak returns a member after the passkey and recovery codes. The
 * language is in the path (/done/de), set by the redirect join asked for. */
(() => {
  const i18n = window.joinI18n;
  // The path carries the language the member last chose on the join page.
  i18n.force(location.pathname.split("/").pop());
  // Keycloak arrives here with the sign-in's authorization code; join has no
  // use for it, and it does not belong in the history or a bookmark.
  if (location.search) history.replaceState(null, "", location.pathname);
  document.getElementById("host").textContent = i18n.vars.host;
  i18n.apply();
  fetch("/api/config", { cache: "no-store" })
    .then((response) => response.json())
    .then((config) => {
      if (config.dashboardUrl) document.getElementById("dashboard").href = config.dashboardUrl;
      if (config.memberNetworkUrl) showMemberNetwork(config.memberNetworkUrl);
    })
    .catch(() => { /* the link falls back to this host */ });

  // The Member Network (docs/adr/0066): the member's devices join it through
  // the Tailscale app, pointed at the community's own server. The steps for
  // the device in hand are opened; the others stay one tap away.
  function showMemberNetwork(server) {
    i18n.vars.server = server;
    document.getElementById("login-command").textContent =
      `tailscale login --login-server=${server}`;
    document.getElementById("guide-apple").href = `${server}/apple`;
    document.getElementById("guide-windows").href = `${server}/windows`;
    const agent = navigator.userAgent;
    const touchMac = /Macintosh/.test(agent) && navigator.maxTouchPoints > 1;
    const platform = /Android/.test(agent) ? "android"
      : (/iPhone|iPad|iPod/.test(agent) || touchMac) ? "ios" : "computer";
    document.querySelector(`#member-network [data-platform="${platform}"]`).open = true;
    const dashboard = document.getElementById("dashboard");
    dashboard.dataset.t = "mn.dashboard";
    document.getElementById("member-network").hidden = false;
    i18n.apply();
  }
})();
