const CYRON_SERVER = "http://127.0.0.1:17872";
const CYRON_STORAGE_KEY = "cyronBridgeConfig";

async function cyronConfig() {
  const stored = await chrome.storage.local.get(CYRON_STORAGE_KEY);
  const value = stored[CYRON_STORAGE_KEY] || {};
  return {
    server: CYRON_SERVER,
    token: typeof value.token === "string" ? value.token.trim() : ""
  };
}

function cyronNormalizeServer(value) {
  const raw = String(value || "").trim();
  if (!raw) return CYRON_SERVER;
  let parsed;
  try {
    parsed = new URL(/^[a-z]+:\/\//i.test(raw) ? raw : `http://${raw}`);
  } catch {
    throw new Error("آدرس پل معتبر نیست.");
  }
  if (parsed.protocol !== "http:") {
    throw new Error("آدرس پل باید با http شروع شود.");
  }
  if (!["127.0.0.1", "localhost"].includes(parsed.hostname)) {
    throw new Error("پل فقط روی همین کامپیوتر اجرا می‌شود.");
  }
  if (parsed.port && parsed.port !== "17872") {
    throw new Error("پورت پل CYRON باید 17872 باشد.");
  }
  return CYRON_SERVER;
}

async function cyronHealth(server, timeout = 4000) {
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), timeout);
  try {
    const response = await fetch(`${cyronNormalizeServer(server)}/v1/health`, {
      method: "GET",
      cache: "no-store",
      credentials: "omit",
      signal: controller.signal
    });
    return response.ok;
  } finally {
    clearTimeout(timer);
  }
}

async function cyronSend(server, token, payload, timeout = 12000) {
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), timeout);
  try {
    const response = await fetch(`${cyronNormalizeServer(server)}/v1/downloads`, {
      method: "POST",
      cache: "no-store",
      credentials: "omit",
      headers: {
        "Content-Type": "application/json",
        "Authorization": `Bearer ${String(token || "").trim()}`
      },
      body: JSON.stringify(payload),
      signal: controller.signal
    });
    let body = {};
    try {
      body = await response.json();
    } catch {}
    if (response.status === 401) {
      throw new Error("توکن پل با توکن CYRON هم‌خوانی ندارد.");
    }
    if (!response.ok) {
      throw new Error(body?.error ? "CYRON این لینک را نپذیرفت." : "افزودن دانلود انجام نشد.");
    }
    return body;
  } catch (error) {
    if (error?.name === "AbortError") {
      throw new Error("CYRON پاسخ نداد. برنامه باز باشد و دوباره تلاش کن.");
    }
    if (error?.message) throw error;
    throw new Error("ارتباط با CYRON برقرار نشد.");
  } finally {
    clearTimeout(timer);
  }
}

function cyronError(error) {
  const message = String(error?.message || "");
  if (/Failed to fetch|NetworkError|Load failed/i.test(message)) {
    return "CYRON در حال اجرا نیست یا پل مرورگر خاموش است.";
  }
  return message || "ارتباط با CYRON برقرار نشد.";
}

async function cyronActiveTabURL() {
  const tabs = await chrome.tabs.query({ active: true, currentWindow: true });
  return String(tabs?.[0]?.url || "");
}

function cyronWebURL(value) {
  try {
    const parsed = new URL(String(value || ""));
    return parsed.protocol === "http:" || parsed.protocol === "https:" ? parsed.href : "";
  } catch {
    return "";
  }
}

function cyronFlashBadge(text, ok) {
  chrome.action.setBadgeBackgroundColor({ color: ok ? "#1f8f63" : "#b64040" });
  chrome.action.setBadgeText({ text });
  chrome.action.setTitle({ title: ok ? "به صف CYRON اضافه شد" : "ارسال به CYRON انجام نشد" });
  setTimeout(() => {
    chrome.action.setBadgeText({ text: "" });
    chrome.action.setTitle({ title: "ارسال به CYRON" });
  }, 3500);
}
