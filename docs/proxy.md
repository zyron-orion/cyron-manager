# Proxy profiles

CYRON Manager downloads through saved **proxy profiles** instead of a single
free-text proxy string. Every download decides which profile to use: it can
inherit the global default, go direct, or pick its own profile.

## Supported protocols

| Protocol  | Notes                                                         |
| --------- | ------------------------------------------------------------- |
| `http`    | Plain HTTP CONNECT proxy. The most common choice.             |
| `https`   | HTTP proxy reached over TLS.                                  |
| `socks5`  | SOCKS5 with local DNS resolution (the client resolves names).  |
| `socks5h` | SOCKS5 with remote DNS resolution (the proxy resolves names).  |

SOCKS4 is intentionally unsupported and is rejected with a clear error.

## Managing profiles

Open **Settings → Proxy**.

1. Click **Add profile**.
2. Fill in a name, the protocol, the host, and the port.
3. Optionally add a username and password. Passwords are encrypted at rest
   with Windows DPAPI and are never returned to the UI. The list only shows
   whether a password is stored.
4. Click **Test connection** to check the profile before saving. The test
   reports latency and the exit IP.

Editing a profile leaves the password field empty on purpose. Selecting a
profile and clicking **Edit** keeps the stored password unless you type a new
one.

## Global behaviour

| Setting              | Meaning                                                        |
| -------------------- | -------------------------------------------------------------- |
| Default proxy        | Used by every download that does not override it.               |
| Fallback proxies     | Tried in order when the default proxy fails.                    |
| Bypass list          | Hosts, IPs, or CIDR ranges that always download directly.       |
| Test URL             | Endpoint used by the connection test. Defaults to `api.ipify.org`. |
| Direct fallback      | When enabled, a direct connection is the last resort if every proxy fails. |

The bypass list accepts comma-separated values. Wildcards such as
`*.example.com` and bare suffixes such as `.internal.test` are normalised to
the domain they cover. CIDR blocks like `10.0.0.0/8` and bare IPs are also
accepted.

## Per-download override

In the **New download** dialog, the Proxy selector offers:

- **Use global settings** (default) — inherit the default proxy and fallbacks.
- **No proxy** — force a direct connection for this download.
- **A named profile** — use exactly that profile, ignoring the default.

Torrent downloads always connect directly; proxies apply to HTTP, HTTPS, and
media downloads.

## Migrating an old proxy

Versions before 1.1.0 stored a single proxy string. On startup, if no profile
exists yet, that value is converted into a profile named "پروکسی قبلی"
("previous proxy") and set as the default. If the conversion fails, the app
shows a warning and keeps running without a proxy rather than silently
dropping the setting.

Saving Settings with a value in the legacy proxy field also converts it into a
profile, so nothing typed into that box is discarded.

## Security notes

- Profile passwords are encrypted with DPAPI, scoped to the current Windows
  user, and are never serialised to the frontend or to bridge responses.
- The connection test can fail for reasons outside the app's control: blocked
  ports, expired credentials, or a test URL that the proxy cannot reach.
- Proxy settings are stored locally in the app database; nothing leaves the
  machine except the download traffic that the proxy itself carries.
