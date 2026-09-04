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

let grants = [];
let apps = [];

function say(el, text, kind) {
  el.textContent = text;
  el.className = `msg ${kind || ""}`;
}

async function json(resp) {
  if (resp.status === 401)
    throw new Error("Your session has ended. Reload the page to sign in again.");
  if (resp.status === 403)
    throw new Error("You do not administer app access.");
  if (!resp.ok) throw new Error(`Request failed (${resp.status}).`);
  return resp.json();
}

const address = (s) => s.replace(/^user:/, "");

function render() {
  const q = $("q").value.trim().toLowerCase();
  const shown = q
    ? grants.filter(
        (g) =>
          address(g.subject).toLowerCase().includes(q) ||
          g.app.toLowerCase().includes(q) ||
          g.role.toLowerCase().includes(q)
      )
    : grants;

  const body = $("rows");
  body.textContent = "";

  for (const g of shown) {
    const tr = document.createElement("tr");

    const person = document.createElement("td");
    person.className = "person";
    person.textContent = address(g.subject);

    const app = document.createElement("td");
    app.textContent = g.app;

    const role = document.createElement("td");
    const tag = document.createElement("span");
    tag.className = "tag";
    tag.textContent = g.role;
    role.append(tag);

    const action = document.createElement("td");
    action.className = "right";
    const btn = document.createElement("button");
    btn.className = "btn quiet";
    btn.textContent = "Remove";
    btn.setAttribute("aria-label", `Remove ${address(g.subject)} from ${g.app}`);
    btn.onclick = () => revoke(g, btn);
    action.append(btn);

    tr.append(person, app, role, action);
    body.append(tr);
  }

  const empty = $("empty");
  empty.hidden = shown.length > 0;
  if (!shown.length) {
    empty.textContent = grants.length
      ? `Nothing matches “${$("q").value.trim()}”.`
      : "Nobody has been given access to anything yet.";
  }
}

async function revoke(g, btn) {
  btn.disabled = true;
  try {
    await json(
      await api("/revoke", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ app: g.app, role: g.role, subject: g.subject }),
      })
    );
    say($("msg"), `Removed ${address(g.subject)} from ${g.app}.`, "ok");
    await load();
  } catch (e) {
    say($("msg"), e.message, "err");
    btn.disabled = false;
  }
}

async function load() {
  const data = await json(await api("/access"));
  grants = data.grants;
  render();
}

// ─── Adding ────────────────────────────────────────────────────────────────
async function openDialog() {
  say($("dlgmsg"), "");
  $("subject").value = "";

  const box = $("apps");
  box.textContent = "";
  for (const a of apps) {
    const label = document.createElement("label");
    const cb = document.createElement("input");
    cb.type = "checkbox";
    cb.value = a;
    label.append(cb, document.createTextNode(a));
    box.append(label);
  }

  // Roles are per app, and the common case is granting the same role
  // everywhere, so offer the roles of the first app rather than blocking on a
  // choice the person has not made yet.
  const sel = $("role");
  sel.textContent = "";
  if (apps.length) {
    const { roles } = await json(await api(`/apps/${encodeURIComponent(apps[0])}/roles`));
    for (const r of roles.length ? roles : ["user"]) sel.append(new Option(r, r));
  }
  $("dlg").showModal();
  $("subject").focus();
}

async function submit(e) {
  const chosen = [...$("apps").querySelectorAll("input:checked")].map((c) => c.value);
  const subject = $("subject").value.trim();

  if (!subject || !chosen.length) {
    e.preventDefault();
    say($("dlgmsg"), "Enter an address and choose at least one application.", "err");
    return;
  }

  e.preventDefault();
  $("save").disabled = true;
  try {
    // Sequential rather than parallel: a partial failure should leave a clear
    // trail of what did happen, not a race of half-applied grants.
    for (const app of chosen) {
      await json(
        await api("/grant", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ app, role: $("role").value, subject }),
        })
      );
    }
    $("dlg").close();
    say($("msg"), `Added ${subject} to ${chosen.join(", ")}.`, "ok");
    await load();
  } catch (err) {
    say($("dlgmsg"), err.message, "err");
  } finally {
    $("save").disabled = false;
  }
}

$("q").oninput = render;
$("add").onclick = () => openDialog().catch((e) => say($("msg"), e.message, "err"));
$("addform").onsubmit = submit;

(async function start() {
  try {
    const me = await json(await api("/me"));
    $("who").textContent = address(me.subject);
    apps = (await json(await api("/apps"))).apps;
    await load();
  } catch (e) {
    say($("msg"), e.message, "err");
  }
})();
