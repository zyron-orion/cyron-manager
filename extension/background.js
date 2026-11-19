importScripts("shared.js");

const LINK_MENU = "cyron-add-link";
const PAGE_MENU = "cyron-add-page";

function installMenus() {
  chrome.contextMenus.removeAll(() => {
    chrome.contextMenus.create({
      id: LINK_MENU,
      title: "افزودن لینک به CYRON",
      contexts: ["link"]
    });
    chrome.contextMenus.create({
      id: PAGE_MENU,
      title: "افزودن این صفحه به CYRON",
      contexts: ["page"]
    });
  });
}

chrome.runtime.onInstalled.addListener(installMenus);
chrome.runtime.onStartup.addListener(installMenus);

chrome.contextMenus.onClicked.addListener(async (info) => {
  const url = info.menuItemId === LINK_MENU ? info.linkUrl : info.pageUrl;
  if (!url) return;
  try {
    const config = await cyronConfig();
    await cyronSend(config.server, config.token, {
      url,
      engine: "auto",
      quality: "best",
      source: "Browser"
    });
    cyronFlashBadge("OK", true);
  } catch (error) {
    cyronFlashBadge("!", false);
    chrome.action.setTitle({ title: cyronError(error) });
  }
});
