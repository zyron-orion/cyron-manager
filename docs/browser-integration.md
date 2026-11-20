# Browser integration

CYRON Manager 1.1.0 connects to one bundled Chrome or Edge extension. The extension has a fixed ID and talks to a local bridge on loopback. There is no pairing token and no manual copy step for authentication.

## How it works

- The bridge listens on `127.0.0.1` only. The default port is `17872`.
- The extension ID is fixed: `fapkmnilijgofgmeakjjeofaaohgdaoe`.
- Protected requests must carry the exact header `Origin: chrome-extension://fapkmnilijgofgmeakjjeofaaohgdaoe`.
- The bridge also validates the `Host` header and sends CORS headers only to that origin.
- Web pages cannot send the protected requests because a browser page cannot set another extension's origin.
- The extension sends `http` and `https` URLs only. Local files, browser pages, and DRM streams are ignored.

The bridge exposes:

| Endpoint | Method | Purpose |
| --- | --- | --- |
| `/v1/health` | GET | Public local health check |
| `/v1/config` | POST | Capture mode, allowed categories, cookie forwarding |
| `/v1/capture/check` | POST | Decide whether a detected link should be captured |
| `/v1/downloads` | POST | Add a browser download |
| `/v1/status` | POST | Recent download states for the popup |

The request body limit is `128 KB`. Capture checks and add calls share a limit of `120` requests per minute.

`/v1/config` and `/v1/status` take an empty JSON object as their body:

```json
{}
```

Chrome can omit the `Origin` header on simple GET requests from an extension page that has host permission. Both endpoints use POST with a JSON body so the browser includes the extension origin. Do not change them back to GET without verifying the header in a real browser.

## Install the extension

1. Open CYRON Manager and go to Settings.
2. Under Browser connection, turn on link receiving. Keep the default port unless it conflicts with another local program.
3. Open the extension folder from the same section.
4. Open `chrome://extensions` in Chrome or `edge://extensions` in Edge.
5. Turn on Developer mode.
6. Click Load unpacked and choose the folder that CYRON opened.
7. Open the extension options. The address is `http://127.0.0.1:17872` by default. If you changed the port in CYRON, use the same port here.
8. Click Test connection.

Chrome and Edge cannot install a ZIP directly. Extract `CYRON-Browser-Extension-1.1.0.zip` first, then load the extracted folder as an unpacked extension. The app also extracts its bundled copy to `%LOCALAPPDATA%\CYRON\browser-extension`.

After updating CYRON, open `chrome://extensions` and reload the extension. The app writes the bundled files in place, but the browser keeps the old background worker until it is reloaded.

## Settings

### Port

The port must be between `1024` and `65535`. Changing it restarts the bridge. Update the extension address after saving the new port; otherwise the extension still points at the old port.

### Capture mode

- `auto` adds matching downloads without asking.
- `ask` shows a notification with Add and Keep. If no answer arrives within one minute, the extension keeps the browser download and resumes it.
- `off` disables automatic capture. The toolbar button and context menu still send the current page or link.

### Categories

The bridge compares the detected MIME type and file extension against the allowed categories: video, audio, archive, program, and other. A disabled category is skipped before it reaches the download queue.

### Site exclusions

The popup can exclude the current site. Excluded hosts are stored in `chrome.storage.local` and checked before auto capture.

### Cookies

Cookie forwarding is optional. The extension reads cookies only when the setting is on and the extension has cookie permission. Cookies are useful for sites that require a signed-in session.

Forwarded cookies are encrypted with Windows DPAPI in the SQLite database, never returned in public task JSON, and cleared when the download completes. Cookie access is not requested at install time and can be removed from the extension options.

## Behaviour

- The toolbar button sends the active page for manual capture.
- The context menu sends a page, link, audio, video, or image when the item is an `http` or `https` URL.
- The extension checks `/v1/config` before capture, so changes in the app apply without reinstalling.
- A failed send leaves the browser download in place instead of cancelling it.
- The extension keeps pending ask requests in `chrome.storage.local`. If the browser restarts, it recovers the request and tries again.
- Duplicate downloads are rejected by the app. The bridge sets `AllowDuplicate` to false even if a request asks otherwise.

## Troubleshooting

### The bridge is off

- Confirm Browser connection is on and the app is running.
- If the port is busy, choose another port between `1024` and `65535`.
- On Windows, another process can occupy the default port. The app reports the bind error in the browser connection status.

### The extension says forbidden

The bridge rejected the request origin. Reload the extension and confirm the fixed ID in `chrome://extensions` is `fapkmnilijgofgmeakjjeofaaohgdaoe`. Do not edit the `key` field in `manifest.json`. A different ID cannot authenticate to the bridge.

### The extension cannot connect

- Check the address in the extension options. It must match the app address exactly, including the port.
- Click Test connection. The health endpoint does not need the extension origin, so a failure here usually means the bridge is off or the port is wrong.
- Reload the extension after an app update.

### Cookies are not sent

- Cookie forwarding must be on in CYRON settings.
- The extension must have cookie permission. Grant it from the extension options.
- The extension only reads cookies for the URL being downloaded.

## Security notes

- The bridge is not a general-purpose local API. It is bound to loopback and the fixed extension origin.
- There is no bridge token to leak or rotate. The extension ID and `Origin` check replace the old token model.
- The private signing key stays outside the repository in `build/extension-key/browser-extension.pem`. The public key is embedded in `manifest.json`.
- The extension requests `downloads`, `notifications`, `storage`, `alarms`, `contextMenus`, and `tabs`. Cookie and web-origin permissions stay optional.
