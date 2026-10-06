async function jget(path) {
  const r = await fetch(path);
  if (!r.ok) throw new Error(await r.text());
  return r.json();
}

async function jpost(path, body) {
  const r = await fetch(path, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body || {}),
  });
  if (!r.ok) throw new Error(await r.text());
  return r.json();
}

function el(id) { return document.getElementById(id); }

/* ——— tabs ——— */
document.querySelectorAll(".tab").forEach((tab) => {
  tab.addEventListener("click", () => {
    document.querySelectorAll(".tab").forEach((t) => t.classList.toggle("is-active", t === tab));
    const id = tab.dataset.tab;
    document.querySelectorAll(".tab-panel").forEach((p) => {
      p.classList.toggle("is-active", p.dataset.panel === id);
    });
    if (id === "path" && lastStatus) {
      requestAnimationFrame(() => drawWires(lastStatus, lastGroups));
    }
  });
});

/* ——— modal ——— */
const modal = {
  root: el("modal-root"),
  title: el("modal-title"),
  body: el("modal-body"),
  extra: el("modal-extra"),
  ok: el("modal-ok"),
  cancel: el("modal-cancel"),
  resolve: null,
};

function closeModal(result) {
  modal.root.hidden = true;
  modal.extra.replaceChildren();
  modal.ok.disabled = false;
  modal.cancel.disabled = false;
  const r = modal.resolve;
  modal.resolve = null;
  if (r) r(result);
}

function openModal({ title, body, okText, danger, extra, hideCancel }) {
  modal.title.textContent = title || "Confirm";
  modal.body.textContent = body || "";
  modal.ok.textContent = okText || "Confirm";
  modal.ok.className = "btn" + (danger ? " danger" : "");
  modal.cancel.hidden = !!hideCancel;
  modal.extra.replaceChildren();
  if (extra) modal.extra.appendChild(extra);
  modal.root.hidden = false;
  modal.ok.focus();
  return new Promise((resolve) => { modal.resolve = resolve; });
}

function confirmModal(title, body, opts) {
  return openModal({
    title,
    body,
    okText: (opts && opts.okText) || "Confirm",
    danger: opts && opts.danger,
    hideCancel: opts && opts.hideCancel,
  });
}

modal.ok.addEventListener("click", () => closeModal(true));
modal.cancel.addEventListener("click", () => closeModal(false));
modal.root.querySelector("[data-modal-dismiss]").addEventListener("click", () => closeModal(false));
document.addEventListener("keydown", (e) => {
  if (e.key === "Escape" && !modal.root.hidden) closeModal(false);
});

/* ——— live health ——— */
function renderLive(health) {
  const dot = el("live-dot");
  const label = el("live-label");
  const detail = el("live-detail");
  const a = health.agent || {};
  const ok = !!health.ok;
  dot.className = "live-dot";
  if (!ok) {
    dot.classList.add("is-bad");
    label.textContent = "offline";
    detail.textContent = "UI unreachable";
    return;
  }
  if (a.unit_active) {
    dot.classList.add("is-ok");
    label.textContent = "agent + service";
  } else if (a.has_net_admin) {
    dot.classList.add("is-ok");
    label.textContent = "agent live";
  } else {
    dot.classList.add("is-warn");
    label.textContent = "agent (dry-run)";
  }
  const bits = [];
  bits.push("pid " + (a.agent_pid || "?"));
  if (a.unit_installed) bits.push("unit " + (a.unit_state || "?"));
  else bits.push("no unit");
  bits.push(a.has_net_admin ? "CAP_NET_ADMIN" : "no netadmin");
  detail.textContent = bits.join(" · ");
}

async function pollHealth() {
  try {
    const h = await jget("/api/health");
    renderLive(h);
    lastHealth = h;
    const rev = h.rev || "";
    // Only pull full UI data when server state actually changed, and never
    // clobber in-progress form edits.
    if (rev && rev !== lastRev && !dpDirty && !settingsDirty && !refreshInFlight) {
      const prev = lastRev;
      lastRev = rev;
      if (prev !== "") {
        refresh().catch(() => {});
      }
    } else if (!lastRev && rev) {
      lastRev = rev;
    }
    return h;
  } catch (err) {
    renderLive({ ok: false });
    return null;
  }
}

/* ——— viz ——— */
function pickPreview(items, max) {
  const list = [...(items || [])];
  list.sort((a, b) => {
    const aw = a.includes("*") ? 0 : 1;
    const bw = b.includes("*") ? 0 : 1;
    if (aw !== bw) return aw - bw;
    return a.localeCompare(b);
  });
  return { shown: list.slice(0, max), rest: list.length - Math.min(max, list.length), all: list };
}

function fillItemList(container, items, max) {
  container.replaceChildren();
  const { shown, rest, all } = pickPreview(items, max);
  if (!all.length) {
    const none = document.createElement("span");
    none.className = "viz-item-line muted";
    none.textContent = "(none)";
    container.appendChild(none);
    return;
  }
  for (const item of shown) {
    const line = document.createElement("span");
    line.className = "viz-item-line";
    line.textContent = item;
    container.appendChild(line);
  }
  if (rest > 0) {
    const more = document.createElement("span");
    more.className = "viz-item-more";
    more.textContent = "+" + rest + " more";
    const tip = document.createElement("div");
    tip.className = "viz-tip";
    tip.textContent = all.join("\n");
    more.appendChild(tip);
    container.appendChild(more);
  }
}

function buildMatchGroups(rules) {
  const byKey = new Map();
  for (const rule of rules || []) {
    const iface = rule.interface || "(auto)";
    const name = rule.name || iface;
    if (rule.domains && rule.domains.length) {
      const key = "domain|" + iface + "|" + name;
      let g = byKey.get(key);
      if (!g) {
        g = { kind: "domain", iface, name, items: [] };
        byKey.set(key, g);
      }
      for (const d of rule.domains) g.items.push(d);
    }
    if (rule.ips && rule.ips.length) {
      const key = "ip|" + iface + "|" + name;
      let g = byKey.get(key);
      if (!g) {
        g = { kind: "ip", iface, name, items: [] };
        byKey.set(key, g);
      }
      for (const ip of rule.ips) g.items.push(ip);
    }
  }
  return [...byKey.values()];
}

function ifaceRoles(name, status, cfg) {
  const roles = [];
  if (name === status.config_iface) roles.push("CONFIG");
  if (name === status.kernel_iface) roles.push("KERNEL");
  const used = (cfg.rules || []).some((r) => r.interface === name);
  if (used) roles.push("pin");
  return roles;
}

function makeMatchCard(g) {
  const card = document.createElement("div");
  card.className = "viz-node viz-match kind-" + g.kind;
  card.dataset.wireTo = "iface:" + g.iface;
  const kind = document.createElement("span");
  kind.className = "viz-kind";
  kind.textContent = g.kind === "domain" ? "domains" : "ips";
  const title = document.createElement("span");
  title.className = "viz-title";
  title.textContent = g.name;
  const body = document.createElement("div");
  body.className = "viz-items";
  fillItemList(body, g.items, 2);
  const arrow = document.createElement("span");
  arrow.className = "viz-to";
  arrow.textContent = "→ " + g.iface;
  card.append(kind, title, body, arrow);
  return card;
}

function renderViz(status, cfg, ifaces) {
  const matchesEl = el("viz-matches");
  const ifacesEl = el("viz-ifaces");
  const wires = el("viz-wires");
  matchesEl.replaceChildren();
  ifacesEl.replaceChildren();
  wires.replaceChildren();

  const groups = buildMatchGroups(cfg.rules);
  const needed = new Set();
  for (const g of groups) needed.add(g.iface);
  if (status.config_iface) needed.add(status.config_iface);
  if (status.kernel_iface) needed.add(status.kernel_iface);

  const shown = [];
  const seen = new Set();
  // CONFIG (default path) first, then others once each — never duplicate as "ethernet" + iface.
  const order = [];
  if (status.config_iface) order.push(status.config_iface);
  for (const g of groups) {
    if (g.iface && !order.includes(g.iface)) order.push(g.iface);
  }
  if (status.kernel_iface && !order.includes(status.kernel_iface)) order.push(status.kernel_iface);
  for (const name of order) {
    if (seen.has(name)) continue;
    const row = (ifaces || []).find((r) => r.name === name);
    shown.push(row || { name, up: false, ipv4: [] });
    seen.add(name);
  }
  for (const name of needed) {
    if (seen.has(name)) continue;
    const row = (ifaces || []).find((r) => r.name === name);
    shown.push(row || { name, up: false, ipv4: [] });
    seen.add(name);
  }

  const allBox = document.createElement("div");
  allBox.className = "viz-all-wrap";
  const allCard = document.createElement("div");
  allCard.className = "viz-node viz-all";
  allCard.id = "node-all-websites";
  allCard.dataset.wireTo = status.config_iface
    ? "iface:" + status.config_iface
    : (status.kernel_iface ? "iface:" + status.kernel_iface : "");
  allCard.append(
    Object.assign(document.createElement("span"), { className: "viz-kind", textContent: "catch-all" }),
    Object.assign(document.createElement("span"), { className: "viz-title", textContent: "all websites" }),
    Object.assign(document.createElement("span"), {
      className: "viz-sub",
      textContent: status.config_iface
        ? "default → " + status.config_iface
        : (status.kernel_iface ? "KERNEL → " + status.kernel_iface : "no default path"),
    })
  );
  allBox.appendChild(allCard);
  const nest = document.createElement("div");
  nest.className = "viz-all-nest";
  if (!groups.length) {
    nest.appendChild(Object.assign(document.createElement("div"), {
      className: "viz-node viz-empty",
      textContent: "No domain / IP pins",
    }));
  } else {
    for (const g of groups) nest.appendChild(makeMatchCard(g));
  }
  allBox.appendChild(nest);
  matchesEl.appendChild(allBox);

  for (const row of shown) {
    const node = document.createElement("div");
    node.className = "viz-node viz-iface";
    node.dataset.wireId = "iface:" + row.name;
    if (row.name === status.config_iface) node.classList.add("is-default");
    if (row.name === status.kernel_iface) node.classList.add("is-kernel");
    const roles = ifaceRoles(row.name, status, cfg);
    node.append(
      Object.assign(document.createElement("span"), { className: "viz-title mono", textContent: row.name }),
      Object.assign(document.createElement("span"), {
        className: "viz-roles",
        textContent: roles.join(" · ") || (row.up ? "up" : "down"),
      }),
      Object.assign(document.createElement("span"), {
        className: "viz-sub mono",
        textContent: (row.ipv4 && row.ipv4[0]) || (row.up ? "up" : "down"),
      })
    );
    ifacesEl.appendChild(node);
  }

  el("flow-meta").textContent =
    `DNS ${status.dns_listen || "—"} · UI ${status.ui_listen || "—"} · pins ${status.pins || "—"} · mode ${status.default_path_mode || "—"}`;
  requestAnimationFrame(() => drawWires(status, groups));
}

function drawWires(status, groups) {
  const viz = el("viz");
  const svg = el("viz-wires");
  if (!viz || !svg || !document.querySelector('.tab-panel[data-panel="path"].is-active')) return;
  svg.replaceChildren();
  const vr = viz.getBoundingClientRect();
  svg.setAttribute("viewBox", `0 0 ${vr.width} ${vr.height}`);
  svg.setAttribute("width", String(vr.width));
  svg.setAttribute("height", String(vr.height));
  const ns = "http://www.w3.org/2000/svg";

  function centerRight(node) {
    const r = node.getBoundingClientRect();
    return { x: r.right - vr.left, y: r.top + r.height / 2 - vr.top };
  }
  function centerLeft(node) {
    const r = node.getBoundingClientRect();
    return { x: r.left - vr.left, y: r.top + r.height / 2 - vr.top };
  }
  function addFlow(fromEl, toEl, tone, delay) {
    if (!fromEl || !toEl) return;
    const a = centerRight(fromEl);
    const b = centerLeft(toEl);
    const mid = (a.x + b.x) / 2;
    const d = `M ${a.x} ${a.y} C ${mid} ${a.y}, ${mid} ${b.y}, ${b.x} ${b.y}`;
    const path = document.createElementNS(ns, "path");
    path.setAttribute("d", d);
    path.setAttribute("class", "wire wire-" + tone);
    path.setAttribute("fill", "none");
    svg.appendChild(path);
    const packet = document.createElementNS(ns, "circle");
    packet.setAttribute("r", "4.5");
    packet.setAttribute("class", "wire-packet wire-packet-" + tone);
    const motion = document.createElementNS(ns, "animateMotion");
    motion.setAttribute("dur", (tone === "ether" ? 1.45 : 2.05) + "s");
    motion.setAttribute("repeatCount", "indefinite");
    motion.setAttribute("begin", (delay || 0) + "s");
    motion.setAttribute("path", d);
    packet.appendChild(motion);
    svg.appendChild(packet);
  }
  function ifaceNode(name) {
    return el("viz-ifaces").querySelector('[data-wire-id="iface:' + name + '"]');
  }
  let i = 0;
  el("viz-matches").querySelectorAll(".viz-match").forEach((card) => {
    const to = (card.dataset.wireTo || "").replace(/^iface:/, "");
    addFlow(card, ifaceNode(to), card.classList.contains("kind-ip") ? "ip" : "domain", i * 0.28);
    i += 1;
  });
  const allNode = el("node-all-websites");
  const catchTo = (allNode && allNode.dataset.wireTo || "").replace(/^iface:/, "")
    || status.config_iface
    || status.kernel_iface
    || "";
  if (catchTo) addFlow(allNode, ifaceNode(catchTo), "ether", 0.05);
}

function renderIfaces(rows, selected) {
  const tb = el("ifaces").querySelector("tbody");
  tb.replaceChildren();

  for (const row of rows) {
    const tr = document.createElement("tr");
    const add = (text, mono) => {
      const td = document.createElement("td");
      if (mono) td.className = "mono";
      td.textContent = text;
      tr.appendChild(td);
    };
    add(row.name, true);
    const tdUp = document.createElement("td");
    tdUp.appendChild(Object.assign(document.createElement("span"), {
      className: "badge " + (row.up ? "badge-on" : "badge-off"),
      textContent: row.up ? "up" : "down",
    }));
    tr.appendChild(tdUp);
    const tdK = document.createElement("td");
    tdK.textContent = "—";
    if (row.kernel) {
      tdK.textContent = "";
      tdK.appendChild(Object.assign(document.createElement("span"), { className: "badge badge-on", textContent: "default-route" }));
    }
    tr.appendChild(tdK);
    const tdC = document.createElement("td");
    tdC.textContent = "—";
    if (row.config) {
      tdC.textContent = "";
      tdC.appendChild(Object.assign(document.createElement("span"), { className: "badge badge-on", textContent: "default-path" }));
    }
    tr.appendChild(tdC);
    add((row.ipv4 || []).join(", ") || "—", true);
    tb.appendChild(tr);
  }

  fillDpIfaceSelect(rows, selected);
}

function fillDpIfaceSelect(rows, selected) {
  const sel = el("dp-iface");
  const keep = dpDirty ? sel.value : (selected || "");
  sel.replaceChildren();
  sel.appendChild(Object.assign(document.createElement("option"), { value: "", textContent: "(disabled)" }));
  for (const row of rows) {
    const opt = document.createElement("option");
    opt.value = row.name;
    opt.textContent = row.name + (row.up ? "" : " (down)");
    sel.appendChild(opt);
  }
  sel.value = keep;
}

function renderRules(cfg) {
  const list = el("rules-list");
  list.replaceChildren();
  const rules = cfg.rules || [];
  if (!rules.length) {
    list.textContent = "No pin rules yet.";
    return;
  }
  for (const rule of rules) {
    const card = document.createElement("div");
    card.className = "rule-card";
    const name = document.createElement("div");
    name.className = "name";
    name.textContent = rule.name || "(unnamed)";
    const meta = document.createElement("div");
    meta.className = "meta-line";
    const parts = ["→ " + rule.interface];
    if (rule.domains && rule.domains.length) parts.push(rule.domains.length + " domain(s)");
    if (rule.ips && rule.ips.length) parts.push(rule.ips.length + " ip(s)");
    if (rule.dns) parts.push("dns " + rule.dns);
    meta.textContent = parts.join(" · ");
    card.append(name, meta);
    list.appendChild(card);
  }
}

/* ——— configs ——— */
let lastStatus = null;
let lastGroups = [];
let selectedCfgPath = "";
let configsCache = { active: "", items: [] };
let lastHealth = null;
let lastRev = "";
let refreshInFlight = false;
let dpDirty = false;
let settingsDirty = false;

function markDpDirty() { dpDirty = true; }
function markSettingsDirty() { settingsDirty = true; }

el("dp-iface").addEventListener("change", markDpDirty);
el("dp-mode").addEventListener("change", markDpDirty);
["set-listen", "set-ui", "set-pins", "set-dpmode", "set-upstream"].forEach((id) => {
  el(id).addEventListener("change", markSettingsDirty);
  el(id).addEventListener("input", markSettingsDirty);
});

el("btn-refresh").addEventListener("click", async () => {
  dpDirty = false;
  settingsDirty = false;
  try {
    await refresh();
  } catch (err) {
    await confirmModal("Refresh failed", String(err.message || err), { okText: "OK", hideCancel: true });
  }
});

function renderConfigList(data) {
  configsCache = data;
  const list = el("cfg-list");
  list.replaceChildren();
  el("cfg-active").textContent = "Active: " + (data.active || "—");
  if (!selectedCfgPath) selectedCfgPath = data.active || "";
  for (const item of data.items || []) {
    const btn = document.createElement("button");
    btn.type = "button";
    btn.className = "cfg-item" + (item.path === selectedCfgPath ? " is-active" : "");
    const left = document.createElement("div");
    left.append(
      Object.assign(document.createElement("div"), { className: "name", textContent: item.name }),
      Object.assign(document.createElement("div"), { className: "path", textContent: item.path })
    );
    btn.appendChild(left);
    if (item.active) {
      btn.appendChild(Object.assign(document.createElement("span"), { className: "cfg-badge", textContent: "selected" }));
    }
    btn.addEventListener("click", () => {
      selectedCfgPath = item.path;
      renderConfigList(configsCache);
    });
    btn.addEventListener("dblclick", () => doSelectConfig(item.path));
    list.appendChild(btn);
  }
}

async function doSelectConfig(path) {
  const ok = await confirmModal("Select config", "Load and apply " + path + "?", { okText: "Select & apply" });
  if (!ok) return;
  await jpost("/api/configs/select", { path });
  selectedCfgPath = path;
  await refresh();
}

el("btn-cfg-apply").addEventListener("click", async () => {
  const ok = await confirmModal("Apply config", "Reload / select into the running agent?", { okText: "Apply" });
  if (!ok) return;
  try {
    if (selectedCfgPath && selectedCfgPath !== configsCache.active) {
      await jpost("/api/configs/select", { path: selectedCfgPath });
    } else {
      await jpost("/api/configs/apply", {});
    }
    await refresh();
  } catch (err) {
    await confirmModal("Apply failed", String(err.message || err), { okText: "OK", hideCancel: true });
  }
});

el("btn-cfg-new").addEventListener("click", async () => {
  const input = document.createElement("input");
  input.type = "text";
  input.value = "local-copy.yaml";
  const ok = await openModal({
    title: "New config",
    body: "Creates configs/<name>.yaml from defaults.",
    okText: "Create",
    extra: input,
  });
  if (!ok) return;
  try {
    const res = await jpost("/api/configs/new", { name: input.value.trim() });
    selectedCfgPath = res.path;
    await refresh();
  } catch (err) {
    await confirmModal("Create failed", String(err.message || err), { okText: "OK", hideCancel: true });
  }
});

el("btn-cfg-edit").addEventListener("click", async () => {
  try {
    if (selectedCfgPath && selectedCfgPath !== configsCache.active) {
      const switchOk = await confirmModal(
        "Edit another config",
        "Select & apply " + selectedCfgPath + " first?",
        { okText: "Select & edit" }
      );
      if (!switchOk) return;
      await jpost("/api/configs/select", { path: selectedCfgPath });
      await refresh();
    }
    const raw = await jget("/api/config/raw");
    const ta = document.createElement("textarea");
    ta.value = raw.yaml || "";
    const ok = await openModal({
      title: "Edit " + (raw.path || "config"),
      body: "Save writes YAML and applies.",
      okText: "Save & apply",
      extra: ta,
    });
    if (!ok) return;
    const confirmSave = await confirmModal("Confirm save", "Overwrite " + raw.path + "?", { okText: "Save & apply" });
    if (!confirmSave) return;
    await jpost("/api/configs/save", { yaml: ta.value });
    await refresh();
  } catch (err) {
    await confirmModal("Edit failed", String(err.message || err), { okText: "OK", hideCancel: true });
  }
});

el("btn-cfg-save-disk").addEventListener("click", async () => {
  try {
    const raw = await jget("/api/config/raw");
    const ok = await confirmModal("Save config", "Rewrite " + raw.path + " and apply?", { okText: "Save & apply" });
    if (!ok) return;
    await jpost("/api/configs/save", { yaml: raw.yaml });
    await refresh();
  } catch (err) {
    await confirmModal("Save failed", String(err.message || err), { okText: "OK", hideCancel: true });
  }
});

el("btn-cfg-delete").addEventListener("click", async () => {
  const path = selectedCfgPath || configsCache.active;
  if (!path) return;
  const ok = await confirmModal("Delete config", "Delete " + path + "?", { okText: "Delete", danger: true });
  if (!ok) return;
  try {
    await jpost("/api/configs/delete", { path });
    if (selectedCfgPath === path) selectedCfgPath = configsCache.active || "";
    await refresh();
  } catch (err) {
    await confirmModal("Delete failed", String(err.message || err), { okText: "OK", hideCancel: true });
  }
});

/* ——— add rule wizard with animated probe ——— */
el("btn-add-rule").addEventListener("click", () => addRuleWizard());

async function addRuleWizard() {
  const fields = document.createElement("div");
  fields.className = "wizard-fields";
  const kind = document.createElement("select");
  kind.innerHTML = '<option value="domain">domain</option><option value="ip">ip / cidr</option>';
  const value = document.createElement("input");
  value.type = "text";
  value.placeholder = "discord.com or *.discord.com";
  const name = document.createElement("input");
  name.type = "text";
  name.placeholder = "optional rule name";
  fields.append(
    wrapLabel("Type", kind),
    wrapLabel("Value", value),
    wrapLabel("Name", name)
  );

  const ok = await openModal({
    title: "Add domain / IP",
    body: "We’ll probe each up interface, then you pick which one should handle this match.",
    okText: "Probe interfaces",
    extra: fields,
  });
  if (!ok) return;
  const kindVal = kind.value;
  const valueVal = value.value.trim();
  if (!valueVal) {
    await confirmModal("Missing value", "Enter a domain or IP.", { okText: "OK", hideCancel: true });
    return;
  }

  const ifaces = await jget("/api/ifaces");
  const up = (ifaces || []).filter((r) => r.up && r.name !== "lo");
  if (!up.length) {
    await confirmModal("No interfaces", "No up interfaces to probe.", { okText: "OK", hideCancel: true });
    return;
  }

  const stage = document.createElement("div");
  stage.className = "probe-stage";
  const rows = new Map();
  for (const iface of up) {
    const row = document.createElement("div");
    row.className = "probe-row";
    row.dataset.iface = iface.name;
    const left = document.createElement("div");
    left.append(
      Object.assign(document.createElement("div"), { className: "iname", textContent: iface.name }),
      Object.assign(document.createElement("div"), { className: "idetail", textContent: "waiting…" })
    );
    const st = Object.assign(document.createElement("span"), { className: "probe-status idle", textContent: "idle" });
    row.append(left, st);
    stage.appendChild(row);
    rows.set(iface.name, { row, detail: left.querySelector(".idetail"), st });
  }

  let picked = "";
  const pickInfo = { current: "" };

  // Show probe modal (OK disabled until pick)
  modal.title.textContent = "Probing " + valueVal;
  modal.body.textContent = "Testing interfaces one by one. Click an OK result to select it.";
  modal.ok.textContent = "Add rule";
  modal.ok.className = "btn";
  modal.ok.disabled = true;
  modal.cancel.hidden = false;
  modal.extra.replaceChildren(stage);
  modal.root.hidden = false;

  const pickPromise = new Promise((resolve) => { modal.resolve = resolve; });

  // Animate probes sequentially
  for (const iface of up) {
    const ui = rows.get(iface.name);
    ui.row.classList.add("is-testing");
    ui.st.className = "probe-status testing";
    ui.st.textContent = "testing";
    ui.detail.textContent = "probing…";
    await sleep(180);
    try {
      const res = await jpost("/api/probe", { kind: kindVal, value: valueVal, iface: iface.name });
      const hit = (res.results && res.results[0]) || { ok: false, detail: "no result" };
      ui.row.classList.remove("is-testing");
      if (hit.ok) {
        ui.row.classList.add("is-ok");
        ui.st.className = "probe-status ok";
        ui.st.textContent = "ok";
        ui.detail.textContent = hit.detail || "ok";
        ui.row.addEventListener("click", () => {
          rows.forEach((v) => v.row.classList.remove("is-picked"));
          ui.row.classList.add("is-picked");
          pickInfo.current = iface.name;
          modal.ok.disabled = false;
        });
        if (!pickInfo.current) {
          // auto-highlight first OK but still require click or allow Add with first OK
          ui.row.classList.add("is-picked");
          pickInfo.current = iface.name;
          modal.ok.disabled = false;
        }
      } else {
        ui.row.classList.add("is-fail");
        ui.st.className = "probe-status fail";
        ui.st.textContent = "fail";
        ui.detail.textContent = hit.detail || "fail";
        // still allow manual pick of fail iface
        ui.row.style.cursor = "pointer";
        ui.row.addEventListener("click", () => {
          rows.forEach((v) => v.row.classList.remove("is-picked"));
          ui.row.classList.add("is-picked");
          pickInfo.current = iface.name;
          modal.ok.disabled = false;
        });
      }
    } catch (err) {
      ui.row.classList.remove("is-testing");
      ui.row.classList.add("is-fail");
      ui.st.className = "probe-status fail";
      ui.st.textContent = "fail";
      ui.detail.textContent = String(err.message || err);
    }
    await sleep(220);
  }

  const confirmed = await pickPromise;
  if (!confirmed) return;
  picked = pickInfo.current;
  if (!picked) {
    await confirmModal("No interface", "Pick an interface first.", { okText: "OK", hideCancel: true });
    return;
  }

  const saveOk = await confirmModal(
    "Save pin rule",
    "Route " + valueVal + " via " + picked + "?",
    { okText: "Save & apply" }
  );
  if (!saveOk) return;

  const payload = {
    name: name.value.trim(),
    interface: picked,
    domains: kindVal === "domain" ? [valueVal] : [],
    ips: kindVal === "ip" ? [valueVal] : [],
  };
  try {
    await jpost("/api/rules/add", payload);
    await refresh();
    document.querySelector('.tab[data-tab="rules"]').click();
  } catch (err) {
    await confirmModal("Add failed", String(err.message || err), { okText: "OK", hideCancel: true });
  }
}

function wrapLabel(text, control) {
  const label = document.createElement("label");
  label.append(text, control);
  return label;
}

function sleep(ms) {
  return new Promise((r) => setTimeout(r, ms));
}

/* ——— settings ——— */
function renderSettings(settings, health) {
  const runtime = el("settings-runtime");
  runtime.replaceChildren();
  const a = (health && health.agent) || {};
  const cards = [
    ["DNS UDP", settings.listen || health.dns_listen || "—"],
    ["UI TCP", settings.ui_listen || health.ui_listen || "—"],
    ["Pins", settings.pins || "—"],
    ["Default path", (settings.default_path || "(off)") + " / " + (settings.default_path_mode || "—")],
    ["Upstream DNS", settings.dns_upstream || "—"],
    ["Config", settings.path || "—"],
    ["Unit", a.unit_installed ? a.unit_state : "not installed"],
    ["Net admin", a.has_net_admin ? "yes" : "no"],
  ];
  for (const [k, v] of cards) {
    const card = document.createElement("div");
    card.className = "stat-card";
    card.append(
      Object.assign(document.createElement("div"), { className: "k", textContent: k }),
      Object.assign(document.createElement("div"), { className: "v", textContent: String(v) })
    );
    runtime.appendChild(card);
  }

  if (!settingsDirty) {
    el("set-listen").value = settings.listen || "";
    el("set-ui").value = settings.ui_listen || "";
    el("set-pins").value = settings.pins || "auto";
    el("set-dpmode").value = settings.default_path_mode || "off";
    el("set-upstream").value = settings.dns_upstream || "";
  }

  const cmds = el("cmd-list");
  cmds.replaceChildren();
  const commands = settings.commands || {};
  const labels = {
    agent_fg: "Start agent (foreground)",
    agent_dry_run: "Start agent dry-run",
    agent_sudo: "Start agent with sudo",
    ctl_run: "ctl run",
    ctl_menu: "ctl menu",
    ctl_doctor: "ctl doctor",
    service_up: "Service up",
    service_down: "Service down",
    service_apply: "Service apply (SIGHUP)",
    service_status: "Service status",
    install_unit: "Install systemd unit",
  };
  for (const [key, cmd] of Object.entries(commands)) {
    const row = document.createElement("div");
    row.className = "cmd-row";
    const left = document.createElement("div");
    left.style.flex = "1";
    left.append(
      Object.assign(document.createElement("div"), {
        className: "path",
        textContent: labels[key] || key,
      }),
      Object.assign(document.createElement("code"), { textContent: cmd })
    );
    left.querySelector(".path").style.marginBottom = "0.25rem";
    const copy = document.createElement("button");
    copy.type = "button";
    copy.className = "btn ghost";
    copy.textContent = "Copy";
    copy.addEventListener("click", async () => {
      try {
        await navigator.clipboard.writeText(cmd);
        copy.textContent = "Copied";
        setTimeout(() => { copy.textContent = "Copy"; }, 1200);
      } catch (_) {
        copy.textContent = "Fail";
      }
    });
    row.append(left, copy);
    cmds.appendChild(row);
  }

  const presets = el("preset-list");
  presets.replaceChildren();
  for (const p of settings.presets || []) {
    const chip = document.createElement("button");
    chip.type = "button";
    chip.className = "preset-chip";
    chip.textContent = p.label || p.id;
    chip.addEventListener("click", async () => {
      if (p.config) {
        selectedCfgPath = p.config;
        await doSelectConfig(p.config);
        return;
      }
      if (p.listen) el("set-listen").value = p.listen;
      if (p.ui) el("set-ui").value = p.ui;
      if (p.pins) el("set-pins").value = p.pins;
      document.querySelector('.tab[data-tab="settings"]').click();
    });
    presets.appendChild(chip);
  }
}

async function loadDoctor() {
  const list = el("doctor-list");
  list.textContent = "Loading…";
  try {
    const rep = await jget("/api/doctor");
    list.replaceChildren();
    for (const f of rep.findings || []) {
      const item = document.createElement("div");
      item.className = "doctor-item";
      item.append(
        Object.assign(document.createElement("div"), {
          className: "lvl lvl-" + (f.level || "info"),
          textContent: (f.level || "info") + " · " + (f.code || ""),
        }),
        Object.assign(document.createElement("div"), { textContent: f.message || "" })
      );
      list.appendChild(item);
    }
    if (!(rep.findings || []).length) list.textContent = "No findings.";
  } catch (err) {
    list.textContent = String(err.message || err);
  }
}

el("btn-doctor").addEventListener("click", () => loadDoctor());

el("settings-form").addEventListener("submit", async (e) => {
  e.preventDefault();
  const ok = await confirmModal(
    "Save settings",
    "Write agent settings to the active config and apply? UI listen changes need an agent restart.",
    { okText: "Save & apply" }
  );
  if (!ok) return;
  const msg = el("set-msg");
  msg.textContent = "Saving…";
  try {
    await jpost("/api/settings", {
      listen: el("set-listen").value.trim(),
      ui_listen: el("set-ui").value.trim(),
      pins: el("set-pins").value,
      default_path_mode: el("set-dpmode").value,
      dns_upstream: el("set-upstream").value.trim(),
    });
    msg.textContent = "Saved. Restart agent if UI listen changed.";
    settingsDirty = false;
    await refresh();
  } catch (err) {
    msg.textContent = String(err.message || err);
  }
});

el("dp-form").addEventListener("submit", async (e) => {
  e.preventDefault();
  const iface = el("dp-iface").value;
  const mode = el("dp-mode").value;
  const ok = await confirmModal(
    "Save default path",
    "Set default_path to " + (iface || "(disabled)") + " (mode " + mode + ")?",
    { okText: "Save" }
  );
  if (!ok) return;
  const msg = el("dp-msg");
  msg.textContent = "Saving…";
  try {
    await jpost("/api/default-path", { interface: iface, mode });
    msg.textContent = "Saved.";
    dpDirty = false;
    await refresh();
  } catch (err) {
    msg.textContent = String(err.message || err);
  }
});

async function refresh() {
  if (refreshInFlight) return;
  refreshInFlight = true;
  try {
    const [status, ifaces, cfg, configs, settings, health] = await Promise.all([
      jget("/api/status"),
      jget("/api/ifaces"),
      jget("/api/config"),
      jget("/api/configs"),
      jget("/api/settings"),
      jget("/api/health"),
    ]);
    lastStatus = status;
    lastGroups = buildMatchGroups(cfg.rules);
    lastHealth = health;
    if (health && health.rev) lastRev = health.rev;
    renderLive(health || { ok: true });
    if (!selectedCfgPath) selectedCfgPath = configs.active || "";
    renderViz(status, cfg, ifaces);
    renderIfaces(ifaces, status.config_iface || "");
    renderConfigList(configs);
    renderRules(cfg);
    renderSettings(settings, health || {});
    if (!dpDirty) {
      el("dp-mode").value = status.default_path_mode || "off";
      el("dp-iface").value = status.config_iface || "";
    }
    el("config-pre").textContent = JSON.stringify(cfg, null, 2);
  } finally {
    refreshInFlight = false;
  }
}

window.addEventListener("resize", () => {
  if (lastStatus) drawWires(lastStatus, lastGroups);
});

refresh()
  .then(() => loadDoctor())
  .catch((err) => {
    el("flow-meta").textContent = "Failed to load: " + err;
    renderLive({ ok: false });
  });

// Lightweight heartbeat only — full UI reload happens on actions or rev change.
setInterval(() => { pollHealth().catch(() => {}); }, 3000);
