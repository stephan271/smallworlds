/* The join page (design §3). No framework, no build step.
 *
 * The invitation code lives in the URL fragment and is only ever POSTed, so it
 * never appears in a server log or a Referer. Every message shown is a key in
 * i18n.js, so switching language re-renders whatever is on screen. */
(() => {
  const i18n = window.joinI18n;
  const $ = (selector) => document.querySelector(selector);
  let code = "";
  let invitation = null;
  let usernameTimer = null;
  let usernameSeq = 0;

  const show = (name) => {
    document.querySelectorAll("[data-view]").forEach((view) => {
      view.hidden = view.dataset.view !== name;
    });
    const heading = $(`[data-view="${name}"] h1`);
    if (heading) { heading.tabIndex = -1; heading.focus({ preventScroll: true }); }
  };

  // Shown with the same normalization the server applies (codes.py).
  const normalizeCode = (text) =>
    text.toUpperCase().replace(/[\s\-_.]/g, "").replace(/O/g, "0").replace(/[IL]/g, "1");
  const displayCode = (c) => (c.length === 10 ? `${c.slice(0, 5)}-${c.slice(5)}` : c);

  const post = async (path, body) => {
    try {
      const response = await fetch(path, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body),
        credentials: "omit",
        cache: "no-store",
      });
      let data = {};
      try { data = await response.json(); } catch (_) { /* empty body */ }
      return { status: response.status, data };
    } catch (_) {
      return { status: 0, data: { error: "unavailable" } };
    }
  };

  const setMessage = (element, key) => {
    if (key) element.dataset.t = key; else delete element.dataset.t;
    element.textContent = key ? i18n.t(key) : "";
  };

  const problem = (key) => {
    setMessage($("#problem-text"), key);
    const transient = key === "unavailable" || key === "too_many";
    $("#retry").hidden = !transient;
    $("#problem-ask").hidden = transient || key === "used";
    show("problem");
  };

  /* Device check: a hint, never a block. Phones by QR code and security keys
   * are invisible to it, so "no platform authenticator" is not "cannot". */
  const checkDevice = async () => {
    const hint = $("#device-hint");
    let key = null;
    if (!window.PublicKeyCredential) {
      key = "device.unsupported";
    } else if (PublicKeyCredential.isUserVerifyingPlatformAuthenticatorAvailable) {
      try {
        if (!(await PublicKeyCredential.isUserVerifyingPlatformAuthenticatorAvailable())) {
          key = "device.no_platform";
        }
      } catch (_) { /* unknown: say nothing */ }
    }
    setMessage(hint, key);
    hint.hidden = !key;
  };

  const open = async () => {
    code = normalizeCode(decodeURIComponent(location.hash.slice(1)));
    if (!code) { show("enter"); $("#code").focus(); return; }
    show("loading");
    const { status, data } = await post("/api/invitation", { code });
    if (status !== 200) { problem(data.error || "unavailable"); return; }
    invitation = data;
    i18n.suggest(data.locale);
    if (data.username) { i18n.vars.username = data.username; i18n.apply(); }
    if (data.kind === "recovery") show("recovery");
    else if (data.state === "started") show("resume");
    else { show("welcome"); checkDevice(); }
  };

  const goToKeycloak = async (body) => {
    show("redirecting");
    const { status, data } = await post("/api/redeem", { code, locale: i18n.lang, ...body });
    if (status === 200 && data.redirect) {
      location.assign(data.redirect);
      return null;
    }
    return { status, data };
  };

  // ---- the profile form -------------------------------------------------

  const fieldStatus = (name) => $(`[data-field="${name}"] .status`);
  const markField = (name, key) => {
    const field = $(`[data-field="${name}"]`);
    field.classList.toggle("invalid", Boolean(key));
    field.querySelector("input").setAttribute("aria-invalid", String(Boolean(key)));
    setMessage(fieldStatus(name), key);
  };

  const checkUsername = async () => {
    const value = $("#username").value.trim().toLowerCase();
    const status = $("#username-status");
    $("[data-field=username]").classList.remove("invalid", "valid");
    if (!value) { setMessage(status, null); return; }
    const seq = ++usernameSeq;
    setMessage(status, "username.checking");
    const answer = await post("/api/username", { code, username: value });
    if (seq !== usernameSeq) return;  // a newer keystroke is already being checked
    if (answer.status !== 200) { setMessage(status, answer.data.error || "unavailable"); return; }
    i18n.vars.username = answer.data.username;
    if (answer.data.available) {
      $("[data-field=username]").classList.add("valid");
      $("#username").setAttribute("aria-invalid", "false");
      setMessage(status, "username.available");
    } else {
      markField("username", answer.data.reason);
    }
  };

  $("#username").addEventListener("input", () => {
    const input = $("#username");
    // Lowercase as they type: the name they see is the name they will use.
    const lower = input.value.toLowerCase();
    if (lower !== input.value) {
      const at = input.selectionStart;
      input.value = lower;
      input.setSelectionRange(at, at);
    }
    clearTimeout(usernameTimer);
    usernameTimer = setTimeout(checkUsername, 350);
  });

  ["firstName", "lastName", "email"].forEach((name) => {
    $(`#${name}`).addEventListener("input", () => markField(name, null));
  });

  $("#profile-form").addEventListener("submit", async (event) => {
    event.preventDefault();
    const values = Object.fromEntries(
      ["username", "firstName", "lastName", "email"].map((n) => [n, $(`#${n}`).value.trim()]));
    let missing = false;
    for (const [name, value] of Object.entries(values)) {
      if (!value) { markField(name, "required"); missing = true; }
    }
    if (missing) { setMessage($("#form-status"), "form.has_problems"); return; }
    setMessage($("#form-status"), null);
    $("#profile-submit").disabled = true;
    const failure = await goToKeycloak(values);
    $("#profile-submit").disabled = false;
    if (!failure) return;
    if (failure.data.fields) {
      show("form");
      for (const [name, key] of Object.entries(failure.data.fields)) markField(name, key);
      setMessage($("#form-status"), "form.has_problems");
      const first = Object.keys(failure.data.fields)[0];
      if (first) $(`#${first}`).focus();
    } else {
      problem(failure.data.error || "unavailable");
    }
  });

  // ---- buttons and navigation ---------------------------------------------

  $("#enter-form").addEventListener("submit", (event) => {
    event.preventDefault();
    const typed = normalizeCode($("#code").value);
    if (!typed) { $("#code").focus(); return; }
    // Setting the fragment keeps the code out of every request line.
    location.hash = displayCode(typed);
  });

  $("#start").addEventListener("click", () => { show("form"); $("#username").focus(); });

  document.querySelectorAll("[data-continue]").forEach((button) => {
    button.addEventListener("click", async () => {
      const failure = await goToKeycloak({});
      if (failure) problem(failure.data.error || "unavailable");
    });
  });

  $("#retry").addEventListener("click", open);
  $("#other-code").addEventListener("click", () => {
    history.replaceState(null, "", location.pathname);
    $("#code").value = "";
    open();
  });
  window.addEventListener("hashchange", open);

  $("#host").textContent = i18n.vars.host;
  i18n.apply();
  open();
})();
