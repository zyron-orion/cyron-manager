const form = document.getElementById("options-form");
const serverInput = document.getElementById("server-url");
const tokenInput = document.getElementById("bridge-token");
const feedback = document.getElementById("feedback");
const testButton = document.getElementById("test-button");

function showFeedback(text, ok = false) {
  feedback.textContent = text;
  feedback.classList.toggle("is-error", !ok);
  feedback.hidden = false;
}

async function init() {
  const config = await cyronConfig();
  serverInput.value = config.server;
  tokenInput.value = config.token;
  if (!config.token) {
    showFeedback("توکن پل هنوز ذخیره نشده است.");
  }
}

form.addEventListener("submit", async (event) => {
  event.preventDefault();
  try {
    const server = cyronNormalizeServer(serverInput.value);
    const token = tokenInput.value.trim();
    if (!token) {
      showFeedback("توکن پل را از تنظیمات CYRON کپی کن.");
      tokenInput.focus();
      return;
    }
    await chrome.storage.local.set({
      [CYRON_STORAGE_KEY]: { server, token }
    });
    serverInput.value = server;
    showFeedback("تنظیمات ذخیره شد.", true);
  } catch (error) {
    showFeedback(cyronError(error));
  }
});

testButton.addEventListener("click", async () => {
  testButton.disabled = true;
  testButton.textContent = "در حال بررسی";
  try {
    const online = await cyronHealth(serverInput.value);
    if (online) showFeedback("پل CYRON فعال است.", true);
    else showFeedback("پل CYRON پاسخ نداد.");
  } catch (error) {
    showFeedback(cyronError(error));
  } finally {
    testButton.disabled = false;
    testButton.textContent = "بررسی اتصال";
  }
});

init();
