const statusBox = document.getElementById("bridge-status");
const statusText = document.getElementById("bridge-status-text");
const form = document.getElementById("send-form");
const input = document.getElementById("download-url");
const engine = document.getElementById("download-engine");
const submit = document.getElementById("send-button");
const feedback = document.getElementById("feedback");
let config = null;

function setStatus(state, text) {
  statusBox.dataset.state = state;
  statusText.textContent = text;
}

function setFeedback(text, ok = false) {
  feedback.textContent = text;
  feedback.classList.toggle("is-error", !ok);
  feedback.hidden = false;
}

async function loadStatus() {
  try {
    const online = await cyronHealth(CYRON_SERVER);
    setStatus(online ? "on" : "off", online ? "پل فعال است" : "پل پاسخ نداد");
    if (!config?.token) {
      setStatus("warn", "توکن تنظیم نشده");
    }
  } catch {
    setStatus("off", "پل در دسترس نیست");
  }
}

async function init() {
  config = await cyronConfig();
  try {
    const url = cyronWebURL(await cyronActiveTabURL());
    if (url) input.value = url;
  } catch {}
  await loadStatus();
  input.focus();
  input.select();
}

form.addEventListener("submit", async (event) => {
  event.preventDefault();
  const url = input.value.trim();
  if (!url) {
    setFeedback("یک لینک معتبر وارد کن.");
    input.focus();
    return;
  }
  submit.disabled = true;
  submit.textContent = "در حال ارسال";
  try {
    await cyronSend(config.server, config.token, {
      url,
      engine: engine.value,
      quality: "best",
      source: "Browser"
    });
    setFeedback("به صف CYRON اضافه شد.", true);
    setStatus("on", "پل فعال است");
  } catch (error) {
    setFeedback(cyronError(error));
  } finally {
    submit.disabled = false;
    submit.textContent = "افزودن به صف";
  }
});

document.getElementById("open-options").addEventListener("click", () => chrome.runtime.openOptionsPage());
init();
