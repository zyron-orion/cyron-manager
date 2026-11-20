const CYRON_CONFIG_KEY = "cyronConfig";
const CYRON_EXCLUDED_KEY = "cyronExcludedHosts";
const CYRON_DEFAULT_SERVER = CyronLib.DEFAULT_SERVER;

function cyronT(key) {
  const value = chrome.i18n.getMessage(key);
  return value || key;
}

function cyronLocaleDirection() {
  const language = String(chrome.i18n.getUILanguage() || "en").toLowerCase();
  return language.startsWith("fa") || language.startsWith("ar") || language.startsWith("he") ? "rtl" : "ltr";
}

function cyronApplyI18n(root = document) {
  if (!root?.querySelectorAll) return;
  root.querySelectorAll("[data-i18n]").forEach((node) => {
    node.textContent = cyronT(node.dataset.i18n);
  });
  root.querySelectorAll("[data-i18n-placeholder]").forEach((node) => {
    node.placeholder = cyronT(node.dataset.i18nPlaceholder);
  });
  root.querySelectorAll("[data-i18n-title]").forEach((node) => {
    node.title = cyronT(node.dataset.i18nTitle);
  });
  root.querySelectorAll("[data-i18n-aria-label]").forEach((node) => {
    node.setAttribute("aria-label", cyronT(node.dataset.i18nAriaLabel));
  });
  const language = cyronLocaleDirection() === "rtl" ? "fa" : "en";
  document.documentElement.lang = language;
  document.documentElement.dir = cyronLocaleDirection();
}

async function cyronConfig() {
  try {
    const stored = await chrome.storage.local.get(CYRON_CONFIG_KEY);
    return { server: CyronLib.normalizeServer(stored[CYRON_CONFIG_KEY]?.server) };
  } catch {
    return { server: CYRON_DEFAULT_SERVER };
  }
}

async function cyronSaveConfig(server) {
  const normalized = CyronLib.normalizeServer(server);
  await chrome.storage.local.set({ [CYRON_CONFIG_KEY]: { server: normalized } });
  return normalized;
}

async function cyronRequest(server, path, options = {}, timeout = 8000) {
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), timeout);
  try {
    const body = options.body === undefined ? undefined : JSON.stringify(options.body);
    const headers = {};
    if (body !== undefined) headers["Content-Type"] = "application/json";
    const response = await fetch(`${CyronLib.normalizeServer(server)}${path}`, {
      method: options.method || "GET",
      mode: "cors",
      cache: "no-store",
      credentials: "omit",
      headers,
      body,
      signal: controller.signal
    });
    let payload = null;
    try {
      payload = await response.json();
    } catch {}
    if (!response.ok) {
      const error = new Error(payload?.error || "request_failed");
      error.status = response.status;
      error.payload = payload;
      throw error;
    }
    return payload;
  } catch (error) {
    if (error?.name === "AbortError") {
      const timeoutError = new Error("timeout");
      timeoutError.status = 0;
      throw timeoutError;
    }
    throw error;
  } finally {
    clearTimeout(timer);
  }
}

async function cyronHealth(server, timeout = 4000) {
  try {
    const payload = await cyronRequest(server, "/v1/health", {}, timeout);
    return Boolean(payload?.ok);
  } catch {
    return false;
  }
}

async function cyronGetConfig(server) {
  return cyronRequest(server, "/v1/config", { method: "POST", body: {} });
}

async function cyronCheck(server, payload) {
  return cyronRequest(server, "/v1/capture/check", { method: "POST", body: payload }, 6000);
}

async function cyronSend(server, payload) {
  return cyronRequest(server, "/v1/downloads", { method: "POST", body: payload }, 15000);
}

async function cyronStatus(server) {
  return cyronRequest(server, "/v1/status", { method: "POST", body: {} }, 6000);
}

function cyronError(error) {
  const message = String(error?.message || "");
  if (message === "invalid_server") return cyronT("errorServer");
  if (error?.status === 403) return cyronT("errorForbidden");
  if (error?.status === 429) return cyronT("errorBusy");
  if (/Failed to fetch|NetworkError|Load failed|timeout/i.test(message)) return cyronT("errorOffline");
  if (message === "request_failed" || error?.status >= 500) return cyronT("errorGeneric");
  if (message === "link is not a valid http or https URL") return cyronT("feedbackInvalid");
  return message || cyronT("errorGeneric");
}

function cyronStatusLabel(status) {
  const key = {
    queued: "statusQueued",
    active: "statusActive",
    paused: "statusPaused",
    completed: "statusCompleted",
    error: "statusError",
    canceled: "statusCanceled"
  }[String(status || "")];
  return key ? cyronT(key) : String(status || "");
}

function cyronModeLabel(mode) {
  const key = { auto: "modeAuto", ask: "modeAsk", off: "modeOff" }[String(mode || "")];
  return key ? cyronT(key) : cyronT("modeAuto");
}

async function cyronCookiePermission() {
  try {
    return await chrome.permissions.contains({
      permissions: ["cookies"],
      origins: ["http://*/*", "https://*/*"]
    });
  } catch {
    return false;
  }
}

async function cyronRequestCookiePermission() {
  try {
    return await chrome.permissions.request({
      permissions: ["cookies"],
      origins: ["http://*/*", "https://*/*"]
    });
  } catch {
    return false;
  }
}

async function cyronRemoveCookiePermission() {
  try {
    return await chrome.permissions.remove({
      permissions: ["cookies"],
      origins: ["http://*/*", "https://*/*"]
    });
  } catch {
    return false;
  }
}

async function cyronCookieHeader(url) {
  if (!CyronLib.isHTTPURL(url) || !(await cyronCookiePermission())) return "";
  try {
    const cookies = await chrome.cookies.getAll({ url });
    return CyronLib.cookieHeader(cookies);
  } catch {
    return "";
  }
}

async function cyronExcludedHosts() {
  try {
    const stored = await chrome.storage.local.get(CYRON_EXCLUDED_KEY);
    return Array.isArray(stored[CYRON_EXCLUDED_KEY]) ? stored[CYRON_EXCLUDED_KEY].filter(Boolean) : [];
  } catch {
    return [];
  }
}

async function cyronSetHostExcluded(host, excluded) {
  const normalized = String(host || "").toLowerCase();
  if (!normalized) return;
  const hosts = new Set(await cyronExcludedHosts());
  if (excluded) hosts.add(normalized);
  else hosts.delete(normalized);
  await chrome.storage.local.set({ [CYRON_EXCLUDED_KEY]: Array.from(hosts).sort() });
}

async function cyronIsExcluded(url) {
  const host = CyronLib.hostFromURL(url);
  if (!host) return false;
  return (await cyronExcludedHosts()).includes(host);
}

function cyronFlashBadge(text, ok) {
  chrome.action.setBadgeBackgroundColor({ color: ok ? "#2f9f6f" : "#b64040" });
  chrome.action.setBadgeText({ text });
  chrome.action.setTitle({ title: cyronT(ok ? "statusOk" : "statusError") });
  setTimeout(() => {
    chrome.action.setBadgeText({ text: "" });
    chrome.action.setTitle({ title: cyronT("actionTitle") });
  }, 3500);
}

async function cyronActiveTab() {
  const tabs = await chrome.tabs.query({ active: true, currentWindow: true });
  return tabs?.[0] || null;
}

function cyronWebURL(value) {
  if (!CyronLib.isHTTPURL(value)) return "";
  try {
    return new URL(String(value)).href;
  } catch {
    return "";
  }
}

function cyronOpenOptions() {
  chrome.runtime.openOptionsPage();
}
