/* The Invitation Desk (design §4). No framework, no build step.
 *
 * Every label, name and username is the Operator's or a member's own text, so
 * all of it goes in through textContent. The one innerHTML is the QR encoder's
 * SVG, which carries path data only. */
(() => {
  const i18n = window.joinI18n;
  const $ = (selector) => document.querySelector(selector);
  const el = (tag, className, text) => {
    const node = document.createElement(tag);
    if (className) node.className = className;
    if (text !== undefined) node.textContent = text;
    return node;
  };
  const translated = (tag, className, key, vars) => {
    const node = el(tag, className);
    node.dataset.t = key;
    if (vars) node.dataset.tVars = JSON.stringify(vars);
    node.textContent = i18n.t(key, vars || {});
    return node;
  };

  let mode = "one";
  let issued = [];
  let invitations = [];
  let selectedMember = null;
  let searchTimer = null;
  let searchSeq = 0;
  let toastTimer = null;

  // ---- plumbing -------------------------------------------------------------

  const call = async (method, path, body) => {
    let response;
    try {
      response = await fetch(path, {
        method,
        credentials: "same-origin",
        cache: "no-store",
        headers: body === undefined ? {} : { "Content-Type": "application/json", "X-Join-Desk": "1" },
        body: body === undefined ? undefined : JSON.stringify(body),
      });
    } catch (_) {
      return { status: 0, data: { error: "unavailable" } };
    }
    if (response.status === 401) {
      location.assign(`/desk/login?lang=${i18n.lang}`);
      return { status: 401, data: { error: "signed_out" } };
    }
    let data = {};
    try { data = await response.json(); } catch (_) { /* empty */ }
    return { status: response.status, data };
  };

  const toast = (key, vars, isError = false) => {
    const box = $("#toast");
    box.dataset.t = key;
    box.dataset.tVars = JSON.stringify(vars || {});
    box.textContent = i18n.t(key, vars || {});
    box.classList.toggle("error", isError);
    box.hidden = false;
    clearTimeout(toastTimer);
    toastTimer = setTimeout(() => { box.hidden = true; }, isError ? 8000 : 2500);
  };
  const fail = (data) => toast(`error.${data.error}` in window.JOIN_TEXT.en
    ? `error.${data.error}` : "error.generic", {}, true);

  const date = (epoch, lang = i18n.lang) =>
    new Intl.DateTimeFormat(lang === "de" ? "de-CH" : "en-GB", { dateStyle: "medium" })
      .format(new Date(epoch * 1000));
  const cardDate = (epoch) =>
    new Intl.DateTimeFormat("de-CH", { dateStyle: "medium" }).format(new Date(epoch * 1000));

  const message = (result) => {
    const invitation = result.invitation;
    const template = window.DESK_MESSAGES[invitation.kind][invitation.locale];
    const values = {
      name: invitation.label ? ` ${invitation.label}` : "",
      host: i18n.vars.host,
      date: date(invitation.expiresAt, invitation.locale),
      link: result.link,
    };
    return template.replace(/\{(\w+)\}/g, (_, key) => values[key] ?? "");
  };

  const qrSvg = (text) => {
    const qr = window.qrcode(0, "M");
    qr.addData(text);
    qr.make();
    return qr.createSvgTag({ cellSize: 4, margin: 4, scalable: true });
  };

  const copy = async (text) => {
    try {
      await navigator.clipboard.writeText(text);
    } catch (_) {
      const area = $("#issued-message");
      area.focus();
      area.select();
      document.execCommand("copy");
    }
    toast("issued.copied");
  };

  // ---- issuing ----------------------------------------------------------------

  const showIssued = (results, removed = 0) => {
    issued = results;
    const section = $("#issued");
    const title = $("#issued-title");
    const first = results[0].invitation;
    let key = "issued.title_unnamed";
    let vars = {};
    if (results.length > 1) { key = "issued.title_many"; vars = { count: results.length }; }
    else if (first.kind === "recovery") { key = "issued.title_recovery"; vars = { label: first.label }; }
    else if (first.label) { key = "issued.title_one"; vars = { label: first.label }; }
    title.dataset.t = key;
    title.dataset.tVars = JSON.stringify(vars);

    const removedNote = $("#issued-removed");
    removedNote.hidden = !removed;
    if (removed) {
      removedNote.dataset.t = "issued.removed";
      removedNote.dataset.tVars = JSON.stringify({ count: removed });
    }

    $("#issued-one").hidden = results.length !== 1;
    $("#issued-many").hidden = results.length === 1;
    if (results.length === 1) {
      const result = results[0];
      $("#issued-qr").innerHTML = qrSvg(result.link);
      $("#issued-code").textContent = result.code;
      $("#issued-link").textContent = result.link;
      const until = $("#issued-until");
      until.dataset.t = "issued.valid_until";
      until.dataset.tVars = JSON.stringify({ date: date(first.expiresAt) });
      $("#issued-message").value = message(result);
    } else {
      const list = $("#issued-list");
      list.replaceChildren(...results.map((result, index) => {
        const item = el("li");
        item.append(el("span", "name", result.invitation.label), el("span", "code", result.code));
        const button = translated("button", "small", "issued.copy_message");
        button.type = "button";
        button.dataset.index = String(index);
        item.append(button);
        return item;
      }));
    }
    section.hidden = false;
    i18n.apply();
    section.scrollIntoView({ behavior: "smooth", block: "start" });
    section.focus({ preventScroll: true });
  };

  const card = (result) => {
    const invitation = result.invitation;
    const joinHost = new URL(result.link).host;
    const node = el("article", "print-card");
    node.append(el("p", "card-host", i18n.vars.host));
    node.append(el("h3", "card-title", i18n.t("card.title")));
    if (invitation.label) node.append(el("p", "card-for", `${i18n.t("card.for")}: ${invitation.label}`));
    const qr = el("div", "card-qr");
    qr.innerHTML = qrSvg(result.link);
    node.append(qr);
    node.append(el("p", "card-code", result.code));
    node.append(el("p", "card-step", i18n.t("card.step_de", { join: joinHost })));
    node.append(el("p", "card-step", i18n.t("card.step_en", { join: joinHost })));
    node.append(el("p", "card-until", i18n.t("card.until", { date: cardDate(invitation.expiresAt) })));
    return node;
  };

  const printCards = (results) => {
    $("#print-area").replaceChildren(...results.map(card));
    window.print();
  };
  window.addEventListener("afterprint", () => $("#print-area").replaceChildren());

  $("#copy-message").addEventListener("click", () => copy($("#issued-message").value));
  $("#copy-link").addEventListener("click", () => copy(issued[0].link));
  $("#print-one").addEventListener("click", () => printCards(issued));
  $("#print-all").addEventListener("click", () => printCards(issued));
  $("#issued-close").addEventListener("click", () => { $("#issued").hidden = true; issued = []; });
  $("#issued-list").addEventListener("click", (event) => {
    const button = event.target.closest("button[data-index]");
    if (button) copy(message(issued[Number(button.dataset.index)]));
  });
  if (navigator.share) {
    $("#share").hidden = false;
    $("#share").addEventListener("click", () => {
      navigator.share({ text: $("#issued-message").value }).catch(() => { /* dismissed */ });
    });
  }

  const setMode = (next) => {
    mode = next;
    $("#tab-one").setAttribute("aria-selected", String(mode === "one"));
    $("#tab-many").setAttribute("aria-selected", String(mode === "many"));
    $("#field-label").hidden = mode !== "one";
    $("#field-labels").hidden = mode !== "many";
    $("#new-submit").dataset.t = mode === "one" ? "new.submit" : "new.submit_many";
    i18n.apply();
  };
  $("#tab-one").addEventListener("click", () => setMode("one"));
  $("#tab-many").addEventListener("click", () => setMode("many"));

  $("#new-form").addEventListener("submit", async (event) => {
    event.preventDefault();
    const form = event.target;
    const settings = {
      locale: form.elements.locale.value,
      days: Number($("#days").value),
      note: $("#note").value.trim(),
    };
    $("#new-submit").disabled = true;
    let answer;
    if (mode === "one") {
      answer = await call("POST", "/desk/api/invitations", { ...settings, label: $("#label").value.trim() });
    } else {
      const labels = $("#labels").value.split("\n").map((x) => x.trim()).filter(Boolean);
      answer = await call("POST", "/desk/api/invitations/bulk", { ...settings, labels });
    }
    $("#new-submit").disabled = false;
    if (answer.status !== 200) { fail(answer.data); return; }
    showIssued(answer.data.issued || [answer.data]);
    $("#label").value = "";
    $("#labels").value = "";
    $("#note").value = "";
    loadList();
  });

  // ---- the list ---------------------------------------------------------------

  const renderList = () => {
    $("#list-empty").hidden = invitations.length > 0;
    $("#invitations").hidden = invitations.length === 0;
    $("#invitation-rows").replaceChildren(...invitations.map((invitation) => {
      const row = el("tr");
      row.dataset.state = invitation.state;
      const name = el("td", "name", invitation.label || "—");
      if (invitation.kind === "recovery") name.append(" ", translated("span", "tag", "list.recovery"));
      if (invitation.note) name.append(el("span", "note", invitation.note));
      row.append(name);
      row.append(el("td", "username", invitation.username || "—"));
      const state = el("td");
      state.append(translated("span", `badge ${invitation.state}`, `state.${invitation.state}`));
      row.append(state);
      row.append(el("td", "until", ["done", "revoked"].includes(invitation.state)
        ? "—" : date(invitation.expiresAt)));
      const cell = el("td");
      const actions = el("div", "actions");
      cell.append(actions);
      const add = (action, key) => {
        const button = translated("button", "small", key);
        button.type = "button";
        button.dataset.action = action;
        button.dataset.id = String(invitation.id);
        actions.append(button);
      };
      if (["open", "started", "expired"].includes(invitation.state)) add("renew", "list.renew");
      if (["open", "started"].includes(invitation.state)
          || (invitation.state === "expired" && invitation.username && invitation.kind === "new")) {
        add("revoke", "list.revoke");
      }
      row.append(cell);
      return row;
    }));
  };

  const loadList = async () => {
    const answer = await call("GET", "/desk/api/invitations");
    if (answer.status !== 200) { fail(answer.data); return; }
    invitations = answer.data.invitations;
    renderList();
  };
  $("#refresh").addEventListener("click", loadList);

  $("#invitation-rows").addEventListener("click", async (event) => {
    const button = event.target.closest("button[data-action]");
    if (!button) return;
    const invitation = invitations.find((x) => String(x.id) === button.dataset.id);
    if (!invitation) return;
    if (button.dataset.action === "renew") {
      const answer = await call("POST", `/desk/api/invitations/${invitation.id}/renew`,
                                { days: Number($("#days").value) || 14 });
      if (answer.status !== 200) fail(answer.data); else showIssued([answer.data]);
      loadList();
      return;
    }
    const dialog = $("#revoke-dialog");
    const canDelete = invitation.kind === "new" && Boolean(invitation.username);
    $("#revoke-delete-row").hidden = !canDelete;
    $("#revoke-delete").checked = true;
    const text = $("#revoke-delete-text");
    text.dataset.t = "revoke.delete";
    text.dataset.tVars = JSON.stringify({ username: invitation.username || "" });
    i18n.apply();
    dialog.returnValue = "";
    dialog.showModal();
    dialog.addEventListener("close", async () => {
      if (dialog.returnValue !== "confirm") return;
      const answer = await call("POST", `/desk/api/invitations/${invitation.id}/revoke`,
                                { deleteAccount: canDelete && $("#revoke-delete").checked });
      if (answer.status !== 200) fail(answer.data);
      loadList();
    }, { once: true });
  });

  // ---- a new passkey for a member ----------------------------------------------

  const search = async () => {
    const query = $("#member-search").value.trim();
    const seq = ++searchSeq;
    if (query.length < 2) {
      $("#members").replaceChildren();
      $("#members-none").hidden = true;
      return;
    }
    const answer = await call("GET", `/desk/api/members?q=${encodeURIComponent(query)}`);
    if (seq !== searchSeq) return;
    if (answer.status !== 200) { fail(answer.data); return; }
    const members = answer.data.members;
    $("#members-none").hidden = members.length > 0;
    $("#members").replaceChildren(...members.map((m) => {
      const item = el("li");
      const button = el("button", "member");
      button.type = "button";
      button.append(el("strong", "", m.username),
                    el("span", "", ` ${[m.firstName, m.lastName].filter(Boolean).join(" ")}`),
                    translated("span", "passkeys", "recovery.passkeys", { count: m.passkeys }));
      button.addEventListener("click", () => {
        selectedMember = m;
        document.querySelectorAll(".member").forEach((b) => b.setAttribute("aria-pressed", "false"));
        button.setAttribute("aria-pressed", "true");
        const selected = $("#recovery-selected");
        selected.dataset.t = "recovery.selected";
        selected.dataset.tVars = JSON.stringify({ name: m.username });
        $("#recovery-form").hidden = false;
        i18n.apply();
      });
      item.append(button);
      return item;
    }));
  };
  $("#member-search").addEventListener("input", () => {
    clearTimeout(searchTimer);
    searchTimer = setTimeout(search, 300);
  });

  $("#recovery-form").addEventListener("submit", async (event) => {
    event.preventDefault();
    if (!selectedMember) return;
    $("#recovery-submit").disabled = true;
    const answer = await call("POST", "/desk/api/recovery", {
      userId: selectedMember.id,
      locale: event.target.elements["recovery-locale"].value,
      days: Number($("#days").value) || 14,
      removePasskeys: $("#remove-passkeys").checked,
    });
    $("#recovery-submit").disabled = false;
    if (answer.status !== 200) { fail(answer.data); return; }
    showIssued([answer.data], answer.data.removedPasskeys);
    selectedMember = null;
    $("#recovery-form").hidden = true;
    $("#member-search").value = "";
    $("#members").replaceChildren();
    loadList();
  });

  // ---- start ------------------------------------------------------------------

  // Dates are formatted, not translated, so the list is redrawn on a switch.
  document.addEventListener("click", (event) => {
    if (event.target.closest("[data-lang]")) {
      renderList();
      if (issued.length === 1) {
        $("#issued-until").dataset.tVars = JSON.stringify({ date: date(issued[0].invitation.expiresAt) });
        i18n.apply();
      }
    }
  });

  (async () => {
    $("#host").textContent = i18n.vars.host;
    i18n.apply();
    const answer = await call("GET", "/desk/api/me");
    if (answer.status !== 200) return;
    $("#me").textContent = answer.data.username;
    $("#days").value = String(answer.data.defaultDays);
    $("#days").max = String(answer.data.maxDays);
    loadList();
  })();
})();
