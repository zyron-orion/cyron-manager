const form = document.getElementById("options-form");
const serverInput = document.getElementById("server-url");
const feedback = document.getElementById("feedback");
const testButton = document.getElementById("test-button");
const connectionStatus = document.getElementById("connection-status");
const connectionStatusText = document.getElementById("connection-status-text");
const connectionNote = document.getElementById("connection-note");
const captureMode = document.getElementById("capture-mode");
const appVersion = document.getElementById("app-version");
const extensionVersion = document.getElementById("extension-version");
const cookieButton = document.getElementById("cookie-button");
const cookieStatus = document.getElementById("cookie-status");
const cookieNote = document.getElementById("cookie-note");
const excludedList = document.getElementById("excluded-list");
const excludedEmpty = document.getElementById("excluded-empty");
const excludedFeedback = document.getElementById("excluded-feedback");
let currentServer = CyronLib.DEFAULT_SERVER;
let lastRemote = null;
let cookieGranted = false;

function showFeedback(text, ok = false) {
  feedback.textContent = text;
  feedback.classList.toggle("is-error", !ok);
  feedback.hidden = false;
}

function clearFeedback() {
  feedback.hidden = true;
  feedback.textContent = "";
}

function setConnection(state, text) {
  connectionStatus.dataset.state = state;
  connectionStatusText.textContent = text;
}

function setText(id, value) {
  document.getElementById(id).textContent = value;
}

function setCookieNote(text, warning = false) {
  cookieNote.textContent = text;
  cookieNote.classList.toggle("is-warn", warning);
}

function setBusy(button, busy, busyKey, idleKey) {
  button.disabled = busy;
  button.textContent = cyronT(busy ? busyKey : idleKey);
}

function clearConnectionPanel() {
  setText("capture-mode", cyronT("unknown"));
  setText("app-version", cyronT("unknown"));
  connectionNote.textContent = cyronT("connectionLoading");
}

async function probe(server) {
  const online = await cyronHealth(server);
  if (!online) return { online: false, remote: null, error: null };
  try {
    return { online: true, remote: await cyronGetConfig(server), error: null };
  } catch (error) {
    return { online: true, remote: null, error };
  }
}

async function applyProbe(result) {
  if (!result.online) {
    setConnection("off", cyronT("bridgeOff"));
    clearConnectionPanel();
    connectionNote.textContent = cyronT("connectionOffline");
    lastRemote = null;
    await updateCookieUI();
    return;
  }
  if (!result.remote) {
    setConnection("warn", cyronT("bridgeRejected"));
    clearConnectionPanel();
    connectionNote.textContent = cyronError(result.error);
    lastRemote = null;
    await updateCookieUI();
    return;
  }
  setConnection("on", cyronT("bridgeOn"));
  setText("capture-mode", cyronModeLabel(result.remote.captureMode));
  setText("app-version", String(result.remote.appVersion || cyronT("unknown")));
  connectionNote.textContent = cyronT("connected");
  lastRemote = result.remote;
  await updateCookieUI();
}

async function refreshConnection(server, busyButton = null) {
  if (busyButton) setBusy(busyButton, true, "testingButton", "testButton");
  setConnection("checking", cyronT("statusChecking"));
  clearConnectionPanel();
  try {
    await applyProbe(await probe(server));
  } finally {
    if (busyButton) setBusy(busyButton, false, "testingButton", "testButton");
  }
}

async function updateCookieUI() {
  cookieGranted = await cyronCookiePermission();
  cookieStatus.textContent = cyronT(cookieGranted ? "cookiePermissionOn" : "cookiePermissionOff");
  cookieButton.textContent = cyronT(cookieGranted ? "cookieRevoke" : "cookieGrant");
  if (lastRemote?.forwardCookies === true) {
    setCookieNote(cyronT(cookieGranted ? "cookieReady" : "cookiePermissionNeeded"), !cookieGranted);
    return;
  }
  if (lastRemote?.forwardCookies === false) {
    setCookieNote(cyronT("cookieNotNeeded"));
    return;
  }
  setCookieNote(cyronT("cookieHintOptions"));
}

async function renderExcluded() {
  const hosts = await cyronExcludedHosts();
  excludedList.replaceChildren();
  excludedEmpty.hidden = hosts.length > 0;
  for (const host of hosts) {
    const item = document.createElement("li");
    item.className = "excluded-item";
    const name = document.createElement("span");
    name.dir = "ltr";
    name.textContent = host;
    const remove = document.createElement("button");
    remove.type = "button";
    remove.className = "text-button";
    remove.textContent = cyronT("removeButton");
    remove.setAttribute("aria-label", `${cyronT("removeButton")}: ${host}`);
    remove.addEventListener("click", async () => {
      remove.disabled = true;
      try {
        await cyronSetHostExcluded(host, false);
        excludedFeedback.textContent = cyronT("siteRemoved");
        excludedFeedback.classList.remove("is-error");
        excludedFeedback.hidden = false;
        await renderExcluded();
      } catch (error) {
        excludedFeedback.textContent = cyronError(error);
        excludedFeedback.classList.add("is-error");
        excludedFeedback.hidden = false;
        remove.disabled = false;
      }
    });
    item.append(name, remove);
    excludedList.append(item);
  }
}

async function init() {
  cyronApplyI18n();
  document.title = cyronT("optionsTitle");
  extensionVersion.textContent = chrome.runtime.getManifest().version;
  const config = await cyronConfig();
  currentServer = config.server;
  serverInput.value = currentServer;
  await refreshConnection(currentServer);
  await renderExcluded();
}

form.addEventListener("submit", async (event) => {
  event.preventDefault();
  clearFeedback();
  const submit = form.querySelector('button[type="submit"]');
  submit.disabled = true;
  try {
    const server = await cyronSaveConfig(serverInput.value);
    currentServer = server;
    serverInput.value = server;
    showFeedback(cyronT("saved"), true);
    await refreshConnection(server);
  } catch (error) {
    showFeedback(cyronError(error));
  } finally {
    submit.disabled = false;
  }
});

testButton.addEventListener("click", async () => {
  clearFeedback();
  let server;
  try {
    server = CyronLib.normalizeServer(serverInput.value);
  } catch (error) {
    showFeedback(cyronError(error));
    serverInput.focus();
    return;
  }
  testButton.disabled = true;
  testButton.textContent = cyronT("testingButton");
  setConnection("checking", cyronT("statusChecking"));
  clearConnectionPanel();
  try {
    const result = await probe(server);
    await applyProbe(result);
    if (!result.online) showFeedback(cyronT("disconnected"), false);
    else if (!result.remote) showFeedback(cyronError(result.error), false);
    else showFeedback(cyronT(server === currentServer ? "connected" : "testUnsaved"), true);
  } catch (error) {
    showFeedback(cyronError(error));
  } finally {
    testButton.disabled = false;
    testButton.textContent = cyronT("testButton");
  }
});

cookieButton.addEventListener("click", async () => {
  cookieButton.disabled = true;
  try {
    if (cookieGranted) {
      const removed = await cyronRemoveCookiePermission();
      showFeedback(cyronT(removed ? "cookieRevoked" : "cookiePermissionMissing"), removed);
    } else {
      const granted = await cyronRequestCookiePermission();
      showFeedback(cyronT(granted ? "cookieGranted" : "cookiePermissionMissing"), granted);
    }
  } catch (error) {
    showFeedback(cyronError(error));
  } finally {
    await updateCookieUI();
    cookieButton.disabled = false;
  }
});

chrome.storage.onChanged.addListener((changes, area) => {
  if (area === "local" && changes[CYRON_EXCLUDED_KEY]) void renderExcluded();
});

init();
