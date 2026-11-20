# Security policy

## Supported versions

Security fixes are applied to the current 1.1.x release line.

## Reporting a vulnerability

Use the repository's Security tab and open a private security advisory. Do not open a public issue for a vulnerability.

Include:

- CYRON Manager version and Windows version.
- Steps to reproduce the issue.
- The affected component, such as the browser bridge, download engine, file handling, token storage, or cookie forwarding.
- Relevant logs with tokens, passwords, private URLs, and personal paths removed.

Do not attach database files, browser profile data, or credentials.

## Browser bridge model

The browser bridge has no pairing token. It is bound to loopback and accepts protected requests only with the exact `Origin` of the bundled extension ID. The extension ID comes from the public key in `browser-extension/manifest.json`; the private key is not distributed and is not part of the repository. Reports that require modifying the extension ID or bypassing the Windows account boundary are out of scope.

Manual task credentials are kept for the download engines but excluded from task JSON. Usernames, passwords, manual cookie headers, and forwarded browser cookies are not returned in task lists, status responses, or add results.

## Scope

In scope:

- The local browser bridge on `127.0.0.1` and its configurable port.
- Extension origin validation, fixed extension ID, host checks, and CORS handling.
- GitHub token and forwarded browser cookie storage, including DPAPI handling.
- Path handling for downloads and file deletion.
- Download engine request handling and redirects.
- The bundled Chrome and Edge extension.

Out of scope:

- Vulnerabilities in third-party tools that are not reachable through CYRON Manager. Report those to the upstream project as well.
- Issues that require a compromised Windows account or administrator access.
- Social engineering.
