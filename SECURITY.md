# Security policy

## Supported versions

Security fixes are applied to the current 1.0.x release line.

## Reporting a vulnerability

Use the repository's Security tab and open a private security advisory. Do not open a public issue for a vulnerability.

Include:

- CYRON Manager version and Windows version.
- Steps to reproduce the issue.
- The affected component, such as the browser bridge, download engine, file handling, or token storage.
- Relevant logs with tokens, passwords, private URLs, and personal paths removed.

Do not attach database files, browser profile data, or credentials.

## Scope

In scope:

- The local browser bridge on `127.0.0.1:17872`.
- Token storage and DPAPI handling.
- Path handling for downloads and file deletion.
- Download engine request handling and redirects.
- The bundled browser extension.

Out of scope:

- Vulnerabilities in third-party tools that are not reachable through CYRON Manager. Report those to the upstream project as well.
- Issues that require a compromised Windows account or administrator access.
- Social engineering.
