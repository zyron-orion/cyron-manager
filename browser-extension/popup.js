const statusBox = document.getElementById("bridge-status");
const statusText = document.getElementById("bridge-status-text");
const form = document.getElementById("send-form");
const input = document.getElementById("download-url");
const engine = document.getElementById("download-engine");
const submit = document.getElementById("send-button");
const feedback = document.getElementById("feedback");
const captureMode = document.getElementById("capture-mode");
const captureHint = document.getElementById("capture-hint");
const siteRow = document.getElementById("site-row");
const siteHost = document.getElementById("site-host");
const siteNote = document.getElementById("site-note");
const excludeSite = document.getElementById("exclude-site");
const recentState = document.getElementById("recent-state");
const recentList = document.getElementById("recent-list");
const recentRetry = document.getElementById("recent-retry");
let activeURL = "";
let activeTitle = "";

function setStatus(state, text) {
  statusBox.dataset.state = state;
  statusText.textContent = text;
}

function setFeedback(text, ok = false) {
  feedback.textContent = text;
  feedback.classList.toggle("is-error", !ok);
  feedback.hidden = false;
}

function clearFeedback() {
  feedback.hidden = true;
  feedback.textContent = "";
}

function setRecentState(text, state = "") {
  recentState.textContent = text;
  recentState.dataset.state = state;
  recentState.hidden = false;
  recentList.replaceChildren();
  recentRetry.hidden = state !== "error";
}

function modeHint(mode) {
  if (mode === "off") return cyronT("captureOff");
  if (mode === "ask") return cyronT("captureAsk");
  if (mode === "auto") return cyronT("captureAuto");
  return cyronT("connectionLoading");
}

function addRecentItem(task) {
  const title = String(task.title || task.id || cyronT("untitled"));
  const item = document.createElement("li");
  item.className = "recent-item";
  if (task.error) item.classList.add("has-error");

  const top = document.createElement("div");
  top.className = "recent-top";
  const name = document.createElement("span");
  name.className = "recent-title";
  name.dir = "auto";
  name.textContent = title;
  const status = document.createElement("span");
  status.className = "recent-status";
  status.textContent = cyronStatusLabel(task.status);
  top.append(name, status);

  const meta = document.createElement("div");
  meta.className = "recent-meta";
  const parts = [];
  if (Number(task.total) > 0) {
    parts.push(`${CyronLib.formatBytes(task.downloaded)} / ${CyronLib.formatBytes(task.total)}`);
  } else if (Number(task.downloaded) > 0) {
    parts.push(CyronLib.formatBytes(task.downloaded));
  }
  if (Number(task.speed) > 0) parts.push(`${CyronLib.formatBytes(task.speed)}/s`);
  meta.textContent = task.error || parts.join(" · ");

  item.append(top, meta);

  const progressValue = Number(task.progress);
  if (Number.isFinite(progressValue) && progressValue > 0 && progressValue < 100 && !task.error) {
    const progress = document.createElement("progress");
    progress.className = "recent-progress";
    progress.max = 100;
    progress.value = Math.max(0, Math.min(100, progressValue));
    progress.setAttribute("aria-label", `${cyronT("progressLabel")}: ${title}`);
    item.append(progress);
  }
  recentList.append(item);
}

function renderRecent(tasks) {
  const list = Array.isArray(tasks) ? tasks.slice(0, 3) : [];
  if (list.length === 0) {
    setRecentState(cyronT("noRecent"), "empty");
    recentRetry.hidden = true;
    return;
  }
  recentState.hidden = true;
  recentRetry.hidden = true;
  recentList.replaceChildren();
  list.forEach(addRecentItem);
}

async function loadRecent(server) {
  setRecentState(cyronT("recentLoading"), "loading");
  try {
    const payload = await cyronStatus(server);
    renderRecent(payload?.tasks);
  } catch (error) {
    setRecentState(cyronError(error), "error");
  }
}

async function refresh() {
  const { server } = await cyronConfig();
  setStatus("checking", cyronT("statusChecking"));
  captureMode.textContent = cyronT("unknown");
  captureHint.textContent = cyronT("connectionLoading");
  setRecentState(cyronT("recentLoading"), "loading");
  const online = await cyronHealth(server);
  if (!online) {
    setStatus("off", cyronT("bridgeOff"));
    captureHint.textContent = cyronT("connectionOffline");
    setRecentState(cyronT("errorOffline"), "error");
    return;
  }
  let remote;
  try {
    remote = await cyronGetConfig(server);
  } catch (error) {
    setStatus("warn", cyronT("bridgeRejected"));
    captureMode.textContent = cyronT("unknown");
    captureHint.textContent = cyronError(error);
    setRecentState(cyronError(error), "error");
    return;
  }
  setStatus("on", cyronT("bridgeOn"));
  captureMode.textContent = cyronModeLabel(remote?.captureMode);
  captureHint.textContent = modeHint(remote?.captureMode);
  await loadRecent(server);
}

async function setupSite(tab) {
  activeURL = cyronWebURL(tab?.url);
  activeTitle = String(tab?.title || "");
  if (!activeURL) {
    siteNote.textContent = cyronT("siteUnavailable");
    siteNote.hidden = false;
    return;
  }
  const host = CyronLib.hostFromURL(activeURL);
  if (!host) {
    siteNote.textContent = cyronT("siteUnavailable");
    siteNote.hidden = false;
    return;
  }
  siteHost.textContent = host;
  excludeSite.checked = await cyronIsExcluded(activeURL);
  siteRow.hidden = false;
  excludeSite.addEventListener("change", async () => {
    const excluded = excludeSite.checked;
    excludeSite.disabled = true;
    try {
      await cyronSetHostExcluded(host, excluded);
      siteNote.textContent = cyronT(excluded ? "feedbackExcluded" : "feedbackIncluded");
      siteNote.hidden = false;
    } catch (error) {
      excludeSite.checked = !excluded;
      siteNote.textContent = cyronError(error);
      siteNote.hidden = false;
    } finally {
      excludeSite.disabled = false;
    }
  });
}

async function init() {
  cyronApplyI18n();
  document.title = cyronT("sendTitle");
  const tab = await cyronActiveTab();
  activeURL = cyronWebURL(tab?.url);
  if (activeURL) input.value = activeURL;
  await setupSite(tab);
  await refresh();
  input.focus();
  input.select();
}

form.addEventListener("submit", async (event) => {
  event.preventDefault();
  clearFeedback();
  const url = cyronWebURL(input.value.trim());
  if (!url) {
    setFeedback(cyronT("feedbackInvalid"));
    input.focus();
    return;
  }
  submit.disabled = true;
  submit.textContent = cyronT("sendingButton");
  try {
    const response = await chrome.runtime.sendMessage({
      type: "cyron-manual-send",
      url,
      engine: engine.value,
      title: activeTitle,
      referrer: activeURL && activeURL !== url ? activeURL : ""
    });
    if (!response?.ok) throw new Error(response?.error || cyronT("errorGeneric"));
    if (response.result?.duplicate) setFeedback(cyronT("feedbackDuplicate"), true);
    else setFeedback(cyronT("feedbackSent"), true);
    setStatus("on", cyronT("bridgeOn"));
    const { server } = await cyronConfig();
    await loadRecent(server);
  } catch (error) {
    setFeedback(cyronError(error));
  } finally {
    submit.disabled = false;
    submit.textContent = cyronT("sendButton");
  }
});

document.getElementById("open-options").addEventListener("click", () => chrome.runtime.openOptionsPage());
recentRetry.addEventListener("click", () => void refresh());
init();
