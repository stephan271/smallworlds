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
    .then((config) => { if (config.dashboardUrl) document.getElementById("dashboard").href = config.dashboardUrl; })
    .catch(() => { /* the link falls back to this host */ });
})();
