// The UI never holds a credential. Every request relies on the session cookie
// the gateway set and the identity headers the proxy stamps on the way through
// — which is what the API authorises on.
const $ = (id) => document.getElementById(id);
const api = (path, opts) =>
  fetch(`/v1/ui${path}`, { credentials: "same-origin", ...opts });

// The console tells an embedded app it is embedded, and which theme to match,
// by postMessage rather than a query parameter — so a theme toggle does not
// reload the frame and throw away whatever the person was part-way through.
// Only messages from the console are honoured; anything can postMessage into a
// frame, and acting on an arbitrary sender would let any page restyle this one.
window.addEventListener("message", (e) => {
  const d = e.data;
  if (!d || d.source !== "cortex") return;
  document.body.classList.toggle("embedded", !!d.embedded);
  if (d.theme) document.documentElement.dataset.theme = d.theme;
});

let grants = [];   // [{subject, app, role}]
let catalog = [];  // [{name, roles: []}]
let editing = null; // the address being edited, or null when adding

const address = (s) => s.replace(/^user:/, "");
const subjectOf = (a) => (a.includes(":") ? a : `user:${a}`);

function say(el, text, kind) {
  el.textContent = text || "";
  el.className = `msg ${text ? kind || "" : ""}`;
}

async function json(resp) {
  if (resp.status === 401)
    throw new Error("Your session has ended. Reload the page to sign in again.");
  if (resp.status === 403) throw new Error("You do not administer app access.");
  if (!resp.ok) throw new Error(`Request failed (${resp.status}).`);
  return resp.json();
}

/* ── Table ────────────────────────────────────────────────────────────────── */

// One row per person. A person with access to four applications is one fact
// about that person; splitting it across four rows made the table longer than
// the thing it described.
function people() {
  const by = new Map();
  for (const g of grants) {
    const name = address(g.subject);
    if (!by.has(name)) by.set(name, []);
    by.get(name).push(g);
  }
  return [...by.entries()].sort((a, b) => a[0].localeCompare(b[0]));
}

function render() {
  const q = $("q").value.trim().toLowerCase();
  // Match the person OR anything they hold, so searching an application still
  // finds the people who can reach it.
  const shown = people().filter(
    ([name, list]) =>
      !q ||
      name.toLowerCase().includes(q) ||
      list.some((g) => g.app.toLowerCase().includes(q) || g.role.toLowerCase().includes(q))
  );

  const body = $("rows");
  body.textContent = "";

  for (const [name, list] of shown) {
    const tr = document.createElement("tr");
    tr.tabIndex = 0;
    tr.setAttribute("role", "button");
    tr.setAttribute("aria-label", `Edit access for ${name}`);
    const open = () => openEditor(name);
    tr.onclick = open;
    tr.onkeydown = (e) => {
      if (e.key === "Enter" || e.key === " ") { e.preventDefault(); open(); }
    };

    const person = document.createElement("td");
    person.className = "person";
    person.textContent = name;

    const apps = document.createElement("td");
    const wrap = document.createElement("div");
    wrap.className = "apps";
    for (const g of [...list].sort((a, b) => a.app.localeCompare(b.app))) {
      const tag = document.createElement("span");
      tag.className = "tag";
      tag.textContent = g.role === "user" ? g.app : `${g.app} · ${g.role}`;
      wrap.append(tag);
    }
    apps.append(wrap);

    const edit = document.createElement("td");
    edit.className = "right muted";
    edit.textContent = "Edit";

    tr.append(person, apps, edit);
    body.append(tr);
  }

  const total = people().length;
  $("summary").textContent =
    q && total ? `${shown.length} of ${total}` : total ? `${total} ${total === 1 ? "person" : "people"}` : "";

  const state = $("state");
  state.hidden = shown.length > 0;
  state.textContent = !total
    ? "Nobody has been given access yet. Use “Add person” to grant someone."
    : `Nothing matches “${$("q").value.trim()}”.`;
}

/* ── Editor ───────────────────────────────────────────────────────────────── */

// Adding and changing are the same task — deciding what one person may reach —
// so they are the same editor. Changes apply on save, which also means removal
// is reversible right up until then, rather than a click that takes effect
// instantly with no chance to reconsider.
function openEditor(name) {
  editing = name || null;
  const held = new Map(
    grants.filter((g) => address(g.subject) === editing).map((g) => [g.app, g.role])
  );

  $("dlgtitle").textContent = editing ? `Access for ${editing}` : "Add person";
  $("subjectfield").hidden = Boolean(editing);
  $("subject").value = "";
  say($("dlgmsg"), "");

  const list = $("applist");
  list.textContent = "";
  for (const app of catalog) {
    const row = document.createElement("div");
    row.className = "approw";

    const label = document.createElement("label");
    const cb = document.createElement("input");
    cb.type = "checkbox";
    cb.dataset.app = app.name;
    cb.checked = held.has(app.name);
    label.append(cb, document.createTextNode(app.name));

    // Roles belong to their application. Offering one list for every app was
    // wrong the moment two applications defined different roles.
    const sel = document.createElement("select");
    sel.dataset.app = app.name;
    sel.setAttribute("aria-label", `Role for ${app.name}`);
    for (const r of app.roles.length ? app.roles : ["user"]) sel.append(new Option(r, r));
    sel.value = held.get(app.name) ?? (app.roles.includes("user") ? "user" : sel.value);
    sel.disabled = !cb.checked;
    cb.onchange = () => (sel.disabled = !cb.checked);

    row.append(label, sel);
    list.append(row);
  }

  $("dlg").showModal();
  (editing ? $("save") : $("subject")).focus();
}

// The intended state, read off the form.
function chosen() {
  return [...$("applist").querySelectorAll('input[type="checkbox"]')]
    .filter((cb) => cb.checked)
    .map((cb) => ({
      app: cb.dataset.app,
      role: $("applist").querySelector(`select[data-app="${CSS.escape(cb.dataset.app)}"]`).value,
    }));
}

async function save() {
  const person = editing || $("subject").value.trim();
  if (!person) {
    say($("dlgmsg"), "Enter an email address.", "err");
    $("subject").focus();
    return;
  }

  const want = chosen();
  const have = grants.filter((g) => address(g.subject) === person);
  const subject = subjectOf(person);

  // Only the difference is written. Re-granting what somebody already has would
  // churn the audit log with changes that changed nothing.
  const toGrant = want.filter((w) => !have.some((h) => h.app === w.app && h.role === w.role));
  const toRevoke = have.filter((h) => !want.some((w) => w.app === h.app && w.role === h.role));

  if (!toGrant.length && !toRevoke.length) {
    $("dlg").close();
    return;
  }

  $("save").disabled = true;
  try {
    // Sequential rather than parallel: a partial failure should leave a clear
    // trail of what did happen, not a race of half-applied writes.
    for (const r of toRevoke) {
      await json(await api("/revoke", post({ app: r.app, role: r.role, subject })));
    }
    for (const g of toGrant) {
      await json(await api("/grant", post({ app: g.app, role: g.role, subject })));
    }
    $("dlg").close();
    say($("msg"), summarise(person, toGrant, toRevoke), "ok");
    await load();
  } catch (e) {
    say($("dlgmsg"), e.message, "err");
  } finally {
    $("save").disabled = false;
  }
}

const post = (body) => ({
  method: "POST",
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify(body),
});

function summarise(person, granted, revoked) {
  const bits = [];
  if (granted.length) bits.push(`added ${granted.map((g) => g.app).join(", ")}`);
  if (revoked.length) bits.push(`removed ${revoked.map((r) => r.app).join(", ")}`);
  return `${person}: ${bits.join("; ")}.`;
}

/* ── Wiring ───────────────────────────────────────────────────────────────── */

async function load() {
  grants = (await json(await api("/access"))).grants;
  render();
}

$("q").oninput = render;
$("add").onclick = () => openEditor(null);
$("save").onclick = () => save().catch((e) => say($("dlgmsg"), e.message, "err"));
// Cancel closes. It is not a form submission, so it cannot be caught by
// validation and refuse to let go — which is exactly what it used to do.
$("cancel").onclick = () => $("dlg").close();

(async function start() {
  $("state").textContent = "Loading…";
  try {
    const me = await json(await api("/me"));
    $("who").textContent = address(me.subject);
    catalog = (await json(await api("/catalog"))).apps;
    await load();
  } catch (e) {
    $("state").hidden = true;
    say($("msg"), e.message, "err");
  }
})();
