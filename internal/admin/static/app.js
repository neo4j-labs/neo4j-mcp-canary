// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

// Vanilla JS, no build step, no framework — matches this repo's
// minimal-dependency style (see AGENTS.md). Talks only to /admin/api/*.

const form = document.getElementById("config-form");
const previewBtn = document.getElementById("preview-btn");
const applyBtn = document.getElementById("apply-btn");
const previewPanel = document.getElementById("preview-panel");
const diffTableBody = document.querySelector("#diff-table tbody");
const tierSummary = document.getElementById("tier-summary");
const applyResult = document.getElementById("apply-result");

let lastPreviewedEdits = null;

function formToEdits() {
  const data = new FormData(form);
  const edits = {};
  for (const el of form.elements) {
    if (!el.name) continue;
    if (el.type === "checkbox") {
      edits[el.name] = el.checked;
    } else if (el.type === "number") {
      edits[el.name] = Number(data.get(el.name) || 0);
    } else {
      edits[el.name] = data.get(el.name) || "";
    }
  }
  return edits;
}

function editsToForm(edits) {
  for (const el of form.elements) {
    if (!el.name || !(el.name in edits)) continue;
    if (el.type === "checkbox") {
      el.checked = !!edits[el.name];
    } else {
      el.value = edits[el.name];
    }
  }
}

async function loadConfig() {
  const res = await fetch("/admin/api/config");
  if (!res.ok) {
    document.querySelector("main").innerHTML = "<p class='err'>Failed to load config: " + res.status + "</p>";
    return;
  }
  const body = await res.json();
  editsToForm(body.config);

  const tbody = document.querySelector("#tools-table tbody");
  tbody.innerHTML = "";
  const toolSelect = document.getElementById("pg-tool");
  toolSelect.innerHTML = "";
  for (const t of body.tools || []) {
    const row = document.createElement("tr");
    row.innerHTML = `<td>${t.name}</td><td>${t.label}</td><td>${t.category}</td><td>${t.readOnly ? "yes" : "no"}</td>`;
    tbody.appendChild(row);

    const opt = document.createElement("option");
    opt.value = t.name;
    opt.textContent = `${t.name} (${t.category})`;
    toolSelect.appendChild(opt);
  }
}

previewBtn.addEventListener("click", async () => {
  const edits = formToEdits();
  const res = await fetch("/admin/api/preview", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(edits),
  });
  if (!res.ok) {
    previewPanel.classList.remove("hidden");
    diffTableBody.innerHTML = "";
    tierSummary.textContent = "Failed to compute preview: " + res.status;
    tierSummary.className = "err";
    applyBtn.disabled = true;
    return;
  }
  const result = await res.json();
  lastPreviewedEdits = edits;

  diffTableBody.innerHTML = "";
  for (const c of result.changes || []) {
    const row = document.createElement("tr");
    row.innerHTML = `<td>${c.field}</td><td>${c.old}</td><td>${c.new}</td>`;
    diffTableBody.appendChild(row);
  }

  if (!result.changes || result.changes.length === 0) {
    tierSummary.textContent = "No changes.";
    tierSummary.className = "";
    applyBtn.disabled = true;
  } else {
    const tierLabels = { instant: "applies instantly", http_bounce: "briefly restarts the HTTP listener", db_rebuild: "rebuilds the Neo4j connection" };
    const summary = (result.tiers || []).map((t) => tierLabels[t] || t).join("; ");
    tierSummary.textContent = summary ? "This change: " + summary + "." : "";
    tierSummary.className = (result.tiers || []).includes("http_bounce") || (result.tiers || []).includes("db_rebuild") ? "err" : "ok";
    applyBtn.disabled = false;
  }
  previewPanel.classList.remove("hidden");
});

applyBtn.addEventListener("click", async () => {
  if (!lastPreviewedEdits) return;
  applyBtn.disabled = true;
  const res = await fetch("/admin/api/apply", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(lastPreviewedEdits),
  });
  const body = await res.json();
  applyResult.classList.remove("hidden");
  if (!res.ok) {
    applyResult.innerHTML = `<p class="err">Apply failed: ${body.error || res.status}</p>`;
  } else {
    applyResult.innerHTML = `<p class="ok">Applied. Tiers: ${(body.tiers || []).join(", ") || "none"}${body.bounced ? " (HTTP listener restarted)" : ""}</p>`;
    lastPreviewedEdits = null;
    await loadConfig();
  }
});

document.getElementById("pg-call-btn").addEventListener("click", async () => {
  const tool = document.getElementById("pg-tool").value;
  const authz = document.getElementById("pg-authz").value;
  const resultEl = document.getElementById("pg-result");
  let args;
  try {
    args = JSON.parse(document.getElementById("pg-args").value || "{}");
  } catch (e) {
    resultEl.textContent = "Invalid JSON arguments: " + e.message;
    return;
  }

  const headers = { "Content-Type": "application/json" };
  if (authz) headers["Authorization"] = authz;

  const res = await fetch("/admin/api/playground/call", {
    method: "POST",
    headers,
    body: JSON.stringify({ tool, arguments: args }),
  });
  const text = await res.text();
  try {
    resultEl.textContent = JSON.stringify(JSON.parse(text), null, 2);
  } catch {
    resultEl.textContent = text;
  }
});

loadConfig();
