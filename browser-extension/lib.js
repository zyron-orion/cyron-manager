(function (root) {
  const DEFAULT_SERVER = "http://127.0.0.1:17872";
  const DEFAULT_PORT = "17872";

  function isHTTPURL(value) {
    try {
      const parsed = new URL(String(value || ""));
      return parsed.protocol === "http:" || parsed.protocol === "https:";
    } catch {
      return false;
    }
  }

  function hostFromURL(value) {
    try {
      const parsed = new URL(String(value || ""));
      if (parsed.protocol !== "http:" && parsed.protocol !== "https:") return "";
      return parsed.hostname.toLowerCase();
    } catch {
      return "";
    }
  }

  function fileName(value) {
    const raw = String(value || "").trim().replaceAll("\\", "/");
    const parts = raw.split("/");
    return parts[parts.length - 1] || "";
  }

  function safeFilename(value) {
    return fileName(value).replace(/[\u0000-\u001f<>:"|?*]/g, "-").replace(/[. ]+$/g, "").slice(0, 180);
  }

  function extensionOf(value) {
    const name = fileName(value).toLowerCase();
    const index = name.lastIndexOf(".");
    return index >= 0 ? name.slice(index + 1) : "";
  }

  function guessCategory(url, mimeType, filename) {
    const mime = String(mimeType || "").toLowerCase().split(";")[0].trim();
    if (mime.startsWith("video/")) return "video";
    if (mime.startsWith("audio/")) return "audio";
    if (["application/zip", "application/x-rar-compressed", "application/vnd.rar", "application/x-7z-compressed", "application/x-tar", "application/gzip", "application/x-bzip2", "application/x-xz"].includes(mime)) return "archive";
    if (["application/vnd.microsoft.portable-executable", "application/x-msdownload", "application/x-msi", "application/vnd.android.package-archive", "application/x-deb", "application/x-rpm"].includes(mime)) return "program";
    const extension = extensionOf(filename || (() => {
      try {
        return new URL(String(url || "")).pathname;
      } catch {
        return "";
      }
    })());
    if (["mp4", "mkv", "webm", "avi", "mov", "m4v", "flv", "ts", "mpeg", "mpg"].includes(extension)) return "video";
    if (["mp3", "m4a", "flac", "wav", "ogg", "opus", "aac"].includes(extension)) return "audio";
    if (["zip", "rar", "7z", "tar", "gz", "bz2", "xz", "iso", "dmg"].includes(extension)) return "archive";
    if (["exe", "msi", "apk", "deb", "rpm", "appimage", "pkg"].includes(extension)) return "program";
    return "other";
  }

  function normalizeServer(value) {
    const raw = String(value || "").trim() || DEFAULT_SERVER;
    let parsed;
    try {
      parsed = new URL(/^[a-z]+:\/\//i.test(raw) ? raw : `http://${raw}`);
    } catch {
      throw new Error("invalid_server");
    }
    if (parsed.protocol !== "http:") throw new Error("invalid_server");
    if (parsed.username || parsed.password || parsed.search || parsed.hash) throw new Error("invalid_server");
    if (parsed.pathname !== "/" && parsed.pathname !== "") throw new Error("invalid_server");
    const host = parsed.hostname.toLowerCase() === "localhost" ? "127.0.0.1" : parsed.hostname;
    if (host !== "127.0.0.1") throw new Error("invalid_server");
    const port = parsed.port || DEFAULT_PORT;
    if (!/^\d+$/.test(port) || Number(port) < 1 || Number(port) > 65535) throw new Error("invalid_server");
    return `http://${host}:${port}`;
  }

  function cookieHeader(cookies) {
    if (!Array.isArray(cookies)) return "";
    return cookies
      .filter((cookie) => cookie && typeof cookie.name === "string" && typeof cookie.value === "string")
      .map((cookie) => `${cookie.name}=${cookie.value}`)
      .join("; ");
  }

  function formatBytes(value) {
    const bytes = Number(value || 0);
    if (!Number.isFinite(bytes) || bytes <= 0) return "0 B";
    const units = ["B", "KB", "MB", "GB", "TB"];
    const index = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1);
    const amount = bytes / 1024 ** index;
    return `${amount >= 10 || index === 0 ? amount.toFixed(0) : amount.toFixed(1)} ${units[index]}`;
  }

  function isCaptureCandidate(url, state) {
    return state !== "complete" && state !== "interrupted" && isHTTPURL(url);
  }

  const api = {
    DEFAULT_SERVER,
    cookieHeader,
    fileName,
    formatBytes,
    guessCategory,
    hostFromURL,
    isCaptureCandidate,
    isHTTPURL,
    normalizeServer,
    safeFilename
  };

  root.CyronLib = api;
  if (typeof module !== "undefined" && module.exports) module.exports = api;
})(typeof globalThis !== "undefined" ? globalThis : this);
