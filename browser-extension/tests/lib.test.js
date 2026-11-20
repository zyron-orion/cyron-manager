const test = require("node:test");
const assert = require("node:assert/strict");
const crypto = require("node:crypto");
const fs = require("node:fs");
const path = require("node:path");
const lib = require("../lib.js");

const root = path.join(__dirname, "..");
const manifest = JSON.parse(fs.readFileSync(path.join(root, "manifest.json"), "utf8"));
const en = JSON.parse(fs.readFileSync(path.join(root, "_locales/en/messages.json"), "utf8"));
const fa = JSON.parse(fs.readFileSync(path.join(root, "_locales/fa/messages.json"), "utf8"));

function extensionID(key) {
  const digest = crypto.createHash("sha256").update(Buffer.from(key, "base64")).digest().subarray(0, 16);
  return Array.from(digest, (byte) => String.fromCharCode(97 + (byte >> 4), 97 + (byte & 15))).join("");
}

test("normalizes local bridge addresses", () => {
  assert.equal(lib.normalizeServer(""), "http://127.0.0.1:17872");
  assert.equal(lib.normalizeServer("127.0.0.1:17872"), "http://127.0.0.1:17872");
  assert.equal(lib.normalizeServer("localhost"), "http://127.0.0.1:17872");
  assert.equal(lib.normalizeServer("http://localhost:19000"), "http://127.0.0.1:19000");
});

test("rejects addresses outside the local bridge", () => {
  for (const value of [
    "https://127.0.0.1:17872",
    "http://192.168.1.10:17872",
    "http://example.com:17872",
    "http://127.0.0.1:17872/path",
    "http://user:pass@127.0.0.1:17872",
    "http://127.0.0.1:0"
  ]) {
    assert.throws(() => lib.normalizeServer(value), /invalid_server/);
  }
});

test("detects web URLs and capture candidates", () => {
  assert.equal(lib.isHTTPURL("https://example.com/file.zip"), true);
  assert.equal(lib.isHTTPURL("blob:https://example.com/id"), false);
  assert.equal(lib.isHTTPURL("data:text/plain,hello"), false);
  assert.equal(lib.isCaptureCandidate("https://example.com/file.zip", "in_progress"), true);
  assert.equal(lib.isCaptureCandidate("https://example.com/file.zip", "complete"), false);
  assert.equal(lib.isCaptureCandidate("blob:https://example.com/id", "in_progress"), false);
});

test("cleans filenames without dropping the extension", () => {
  assert.equal(lib.fileName("C:\\Users\\me\\report.zip"), "report.zip");
  assert.equal(lib.safeFilename("a:b?.zip"), "a-b-.zip");
  assert.equal(lib.safeFilename("  file.iso...  "), "file.iso");
  assert.equal(lib.safeFilename(""), "");
});

test("guesses categories from MIME type and extension", () => {
  assert.equal(lib.guessCategory("https://example.com/a", "video/mp4", ""), "video");
  assert.equal(lib.guessCategory("https://example.com/a", "application/zip", ""), "archive");
  assert.equal(lib.guessCategory("https://example.com/a", "", "setup.exe"), "program");
  assert.equal(lib.guessCategory("https://example.com/song.flac", "", ""), "audio");
  assert.equal(lib.guessCategory("https://example.com/page", "text/html", ""), "other");
});

test("builds a cookie header and ignores malformed entries", () => {
  assert.equal(lib.cookieHeader([{ name: "sid", value: "abc" }, { name: "theme", value: "dark" }]), "sid=abc; theme=dark");
  assert.equal(lib.cookieHeader([{ name: "sid" }, { name: "ok", value: 3 }]), "");
  assert.equal(lib.cookieHeader(null), "");
});

test("formats byte sizes", () => {
  assert.equal(lib.formatBytes(0), "0 B");
  assert.equal(lib.formatBytes(1024), "1.0 KB");
  assert.equal(lib.formatBytes(10485760), "10 MB");
});

test("keeps the fixed extension ID in the manifest key", () => {
  assert.equal(manifest.version, "1.1.0");
  assert.equal(extensionID(manifest.key), "fapkmnilijgofgmeakjjeofaaohgdaoe");
});

test("keeps the English and Persian locale keys in sync", () => {
  assert.deepEqual(Object.keys(en).sort(), Object.keys(fa).sort());
  for (const [key, value] of Object.entries(en)) {
    assert.ok(value.message, `missing English message for ${key}`);
    assert.ok(fa[key].message, `missing Persian message for ${key}`);
  }
});

test("loads the shared library before shared.js in each page", () => {
  for (const page of ["popup.html", "options.html"]) {
    const html = fs.readFileSync(path.join(root, page), "utf8");
    assert.ok(html.indexOf('src="lib.js"') < html.indexOf('src="shared.js"'), `${page} script order`);
  }
  const background = fs.readFileSync(path.join(root, "background.js"), "utf8");
  assert.match(background, /importScripts\("lib\.js", "shared\.js"\)/);
});

test("uses POST for protected bridge reads", () => {
  const shared = fs.readFileSync(path.join(root, "shared.js"), "utf8");
  for (const name of ["cyronGetConfig", "cyronStatus"]) {
    const match = shared.match(new RegExp(`async function ${name}\\(server\\) \\{[\\s\\S]*?\\n\\}`));
    assert.ok(match, `${name} missing`);
    assert.match(match[0], /method: "POST"/, `${name} must use POST`);
    assert.match(match[0], /body: \{\}/, `${name} must send a JSON body`);
  }
});

test("keeps em dashes out of extension copy", () => {
  for (const file of ["manifest.json", "_locales/en/messages.json", "_locales/fa/messages.json", "popup.html", "options.html"]) {
    const text = fs.readFileSync(path.join(root, file), "utf8");
    assert.equal(text.includes("\u2014"), false, `${file} contains an em dash`);
  }
});
