importScripts("lib.js", "shared.js");

const CYRON_LINK_MENU = "cyron-send-link";
const CYRON_PAGE_MENU = "cyron-send-page-menu";
const CYRON_PENDING_PREFIX = "cyronPending:";
const CYRON_ASK_PREFIX = "cyronAsk:";
const CYRON_ALARM_PREFIX = "cyron-ask-";

function installMenus() {
  chrome.contextMenus.removeAll(() => {
    chrome.contextMenus.create({
      id: CYRON_LINK_MENU,
      title: cyronT("menuLink"),
      contexts: ["link"]
    });
    chrome.contextMenus.create({
      id: CYRON_PAGE_MENU,
      title: cyronT("menuPage"),
      contexts: ["page"]
    });
  });
}

async function ensureConfig() {
  const stored = await chrome.storage.local.get(CYRON_CONFIG_KEY);
  if (!stored[CYRON_CONFIG_KEY]?.server) {
    await chrome.storage.local.set({ [CYRON_CONFIG_KEY]: { server: CYRON_DEFAULT_SERVER } });
  }
}

async function findDownload(id) {
  try {
    const items = await chrome.downloads.search({ id });
    return items?.[0] || null;
  } catch {
    return null;
  }
}

async function pauseDownload(id) {
  const item = await findDownload(id);
  if (!item || item.state !== "in_progress") return false;
  try {
    await chrome.downloads.pause(id);
    return true;
  } catch {
    return false;
  }
}

async function resumeDownload(id) {
  try {
    await chrome.downloads.resume(id);
  } catch {}
}

async function cancelDownload(id) {
  try {
    await chrome.downloads.cancel(id);
  } catch {}
  try {
    await chrome.downloads.erase({ id });
  } catch {}
}

async function notify(id, options) {
  try {
    await chrome.notifications.create(id, {
      type: "basic",
      iconUrl: chrome.runtime.getURL("icons/icon128.png"),
      priority: 1,
      ...options
    });
  } catch {}
}

async function takeDownload(id, server, payload, filename, successNotice = false) {
  try {
    const result = await cyronSend(server, payload);
    await cancelDownload(id);
    cyronFlashBadge("OK", true);
    if (successNotice) {
      await notify(`cyron-added-${id}`, {
        title: cyronT("notifyAdded"),
        message: filename || cyronT("notifyAddedBody")
      });
    }
    return result;
  } catch (error) {
    await resumeDownload(id);
    cyronFlashBadge("!", false);
    await notify(`cyron-error-${id}`, {
      title: cyronT("notifyErrorTitle"),
      message: `${filename ? `${filename}\n` : ""}${cyronError(error)}`
    });
    throw error;
  }
}

async function resolvePending(downloadId, accepted, notificationId = "") {
  const key = `${CYRON_PENDING_PREFIX}${downloadId}`;
  const stored = await chrome.storage.local.get(key);
  const pending = stored[key];
  if (!pending) {
    if (notificationId) await chrome.notifications.clear(notificationId);
    return;
  }
  await chrome.storage.local.remove(key);
  await chrome.alarms.clear(`${CYRON_ALARM_PREFIX}${downloadId}`);
  if (notificationId) await chrome.notifications.clear(notificationId);
  if (!accepted) {
    await resumeDownload(downloadId);
    await notify(`cyron-kept-${downloadId}`, {
      title: cyronT("notifyKept"),
      message: pending.filename || cyronT("notifyKeptBody")
    });
    return;
  }
  const current = await findDownload(downloadId);
  if (!current || current.state !== "in_progress") return;
  try {
    await takeDownload(downloadId, pending.server, pending.payload, pending.filename, true);
  } catch {}
}

async function askDownload(id, server, payload, filename) {
  const notificationId = `${CYRON_ASK_PREFIX}${id}`;
  await chrome.storage.local.set({
    [`${CYRON_PENDING_PREFIX}${id}`]: {
      server,
      payload,
      filename,
      createdAt: Date.now()
    }
  });
  try {
    await chrome.notifications.create(notificationId, {
      type: "basic",
      iconUrl: chrome.runtime.getURL("icons/icon128.png"),
      title: cyronT("askTitle"),
      message: filename || cyronT("askMessage"),
      priority: 2,
      buttons: [
        { title: cyronT("askAdd") },
        { title: cyronT("askKeep") }
      ]
    });
  } catch {
    await chrome.storage.local.remove(`${CYRON_PENDING_PREFIX}${id}`);
    await resumeDownload(id);
    throw new Error("notification_failed");
  }
  await chrome.alarms.create(`${CYRON_ALARM_PREFIX}${id}`, { delayInMinutes: 1 });
}

async function captureDownload(item) {
  if (!item || item.byExtensionId === chrome.runtime.id) return;
  if (!CyronLib.isCaptureCandidate(item.url, item.state)) return;
  if (await cyronIsExcluded(item.url)) return;
  const { server } = await cyronConfig();
  let remote;
  try {
    remote = await cyronGetConfig(server);
  } catch {
    return;
  }
  if (!remote || remote.captureMode === "off") return;
  const filename = CyronLib.safeFilename(item.filename);
  let check;
  try {
    check = await cyronCheck(server, {
      url: item.url,
      mimeType: item.mime || "",
      filename,
      totalBytes: Number(item.totalBytes || 0)
    });
  } catch {
    return;
  }
  if (!check || check.action === "skip") return;
  if (!(await pauseDownload(item.id))) return;
  const current = await findDownload(item.id);
  if (!current || current.state !== "in_progress") {
    await resumeDownload(item.id);
    return;
  }
  const url = cyronWebURL(current.finalUrl) || cyronWebURL(current.url) || item.url;
  const cookies = remote.forwardCookies ? await cyronCookieHeader(url) : "";
  const payload = {
    url,
    engine: "auto",
    quality: "best",
    title: filename,
    filename,
    referrer: current.referrer || "",
    userAgent: navigator.userAgent,
    mimeType: current.mime || item.mime || "",
    cookies,
    source: "Browser"
  };
  if (check.action === "ask") {
    try {
      await askDownload(item.id, server, payload, filename);
    } catch {
      await resumeDownload(item.id);
    }
    return;
  }
  try {
    await takeDownload(item.id, server, payload, filename, false);
  } catch {}
}

async function manualSend(input) {
  const url = cyronWebURL(input?.url);
  if (!url) throw new Error(cyronT("feedbackInvalid"));
  const { server } = await cyronConfig();
  let remote = null;
  try {
    remote = await cyronGetConfig(server);
  } catch {}
  const cookies = remote?.forwardCookies ? await cyronCookieHeader(url) : "";
  return cyronSend(server, {
    url,
    engine: input.engine || "auto",
    quality: "best",
    title: input.title || "",
    referrer: input.referrer || "",
    userAgent: navigator.userAgent,
    mimeType: "",
    cookies,
    source: "Browser"
  });
}

async function recoverPending() {
  const stored = await chrome.storage.local.get(null);
  const keys = Object.keys(stored).filter((key) => key.startsWith(CYRON_PENDING_PREFIX));
  for (const key of keys) {
    const id = Number(key.slice(CYRON_PENDING_PREFIX.length));
    if (Number.isFinite(id)) await resumeDownload(id);
    await chrome.storage.local.remove(key);
  }
}

async function sendCurrentTab(tab) {
  const current = tab || (await cyronActiveTab());
  const url = cyronWebURL(current?.url);
  if (!url) return;
  try {
    await manualSend({ url, title: current.title || "", referrer: current.url || "" });
    cyronFlashBadge("OK", true);
  } catch {
    cyronFlashBadge("!", false);
  }
}

chrome.runtime.onInstalled.addListener(() => {
  installMenus();
  void ensureConfig();
});

chrome.runtime.onStartup.addListener(() => {
  installMenus();
  void ensureConfig();
  void recoverPending();
});

chrome.contextMenus.onClicked.addListener((info, tab) => {
  const url = info.menuItemId === CYRON_LINK_MENU ? info.linkUrl : info.pageUrl;
  if (!url) return;
  void manualSend({
    url,
    engine: "auto",
    title: info.selectionText || tab?.title || "",
    referrer: tab?.url || ""
  })
    .then(() => cyronFlashBadge("OK", true))
    .catch(() => cyronFlashBadge("!", false));
});

chrome.commands.onCommand.addListener((command, tab) => {
  if (command === "cyron-send-page") void sendCurrentTab(tab);
});

chrome.runtime.onMessage.addListener((message, sender, sendResponse) => {
  if (message?.type !== "cyron-manual-send") return;
  void manualSend(message)
    .then((result) => sendResponse({ ok: true, result }))
    .catch((error) => sendResponse({ ok: false, error: cyronError(error) }));
  return true;
});

chrome.downloads.onCreated.addListener((item) => {
  void captureDownload(item);
});

chrome.notifications.onButtonClicked.addListener((notificationId, buttonIndex) => {
  if (!notificationId.startsWith(CYRON_ASK_PREFIX)) return;
  const id = Number(notificationId.slice(CYRON_ASK_PREFIX.length));
  if (!Number.isFinite(id)) return;
  void resolvePending(id, buttonIndex === 0, notificationId);
});

chrome.notifications.onClosed.addListener((notificationId) => {
  if (!notificationId.startsWith(CYRON_ASK_PREFIX)) return;
  const id = Number(notificationId.slice(CYRON_ASK_PREFIX.length));
  if (!Number.isFinite(id)) return;
  void resolvePending(id, false, "");
});

chrome.alarms.onAlarm.addListener((alarm) => {
  if (!alarm.name.startsWith(CYRON_ALARM_PREFIX)) return;
  const id = Number(alarm.name.slice(CYRON_ALARM_PREFIX.length));
  if (!Number.isFinite(id)) return;
  void resolvePending(id, false, `${CYRON_ASK_PREFIX}${id}`);
});
