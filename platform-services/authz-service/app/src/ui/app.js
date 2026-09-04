// The UI never holds a credential. Every request relies on the session cookie
// the gateway set, and the identity headers the proxy stamps on the way
// through — which is also what the API authorises on.
const $ = (id) => document.getElementById(id);

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
const api = (path, opts) =>
  fetch(`/v1/ui${path}`, { credentials: "same-origin", ...opts });

function say(text, kind) {
  const el = $("msg");
  el.textContent = text;
  el.className = `msg ${kind}`;
}

async function json(resp) {
  if (resp.status === 401)
    throw new Error("Your session has ended. Reload the page to sign in again.");
  if (resp.status === 403)
    throw new Error("You do not administer app access.");
  if (!resp.ok) throw new Error(`Request failed (${resp.status}).`);
  return resp.json();
}

async function loadMembers() {
  const app = $("app").value, role = $("role").value;
  const list = $("members");
  list.innerHTML = "";
  if (!app || !role) return;

  const { members } = await json(
    await api(`/apps/${encodeURIComponent(app)}/roles/${encodeURIComponent(role)}/members`)
  );
  $("count").textContent = members.length
    ? `${members.length} ${members.length === 1 ? "person" : "people"}`
    : "";
  if (!members.length) {
    const li = document.createElement("li");
    li.innerHTML = `<span class="empty">Nobody has this role yet.</span>`;
    list.append(li);
    return;
  }
  for (const m of members) {
    const li = document.createElement("li");
    const name = document.createElement("span");
    // Subjects are stored as user:<address>; show the address.
    name.textContent = m.replace(/^user:/, "");
    const btn = document.createElement("button");
    btn.className = "btn quiet";
    btn.textContent = "Remove";
    btn.setAttribute("aria-label", `Remove ${name.textContent}`);
    btn.onclick = async () => {
      btn.disabled = true;
      try {
        await json(await api("/revoke", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ app, role, subject: m }),
        }));
        say(`Removed ${name.textContent}.`, "ok");
        await loadMembers();
      } catch (e) {
        say(e.message, "err");
        btn.disabled = false;
      }
    };
    li.append(name, btn);
    list.append(li);
  }
}

async function loadRoles() {
  const app = $("app").value;
  const sel = $("role");
  sel.innerHTML = "";
  if (!app) return;
  const { roles } = await json(await api(`/apps/${encodeURIComponent(app)}/roles`));
  for (const r of roles) sel.append(new Option(r, r));
  await loadMembers();
}

async function start() {
  try {
    const me = await json(await api("/me"));
    $("who").textContent = me.subject.replace(/^user:/, "");

    const { apps } = await json(await api("/apps"));
    if (!apps.length) {
      say("No applications have roles defined yet.", "err");
      return;
    }
    for (const a of apps) $("app").append(new Option(a, a));
    await loadRoles();
  } catch (e) {
    $("who").textContent = "";
    say(e.message, "err");
  }
}

$("app").onchange = () => loadRoles().catch((e) => say(e.message, "err"));
$("role").onchange = () => loadMembers().catch((e) => say(e.message, "err"));

$("grant").onclick = async () => {
  const subject = $("subject").value.trim();
  if (!subject) return say("Enter an address to grant access to.", "err");
  $("grant").disabled = true;
  try {
    await json(await api("/grant", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ app: $("app").value, role: $("role").value, subject }),
    }));
    say(`Granted ${subject}.`, "ok");
    $("subject").value = "";
    await loadMembers();
  } catch (e) {
    say(e.message, "err");
  } finally {
    $("grant").disabled = false;
  }
};

start();
