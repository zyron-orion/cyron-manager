# Contributing

## Setup

Use Windows 10 or 11 with Go 1.26 or newer, Wails v2.17.1, and PowerShell 5.1 or newer.

```powershell
powershell -ExecutionPolicy Bypass -File scripts/fetch-tools.ps1
wails dev
```

The frontend is static HTML, CSS, and JavaScript under `frontend/dist`. There is no Node build step.

## Checks

Run before opening a pull request:

```powershell
go test ./...
go vet ./...
```

For UI changes, test the desktop window at its minimum width and at a tablet width. Test Persian and English, plus light and dark themes. Keep both language tables in `frontend/dist/app.js` in sync.

## Pull requests

- Keep the change focused on one fix or feature.
- Do not commit `bin/*.exe`, `build/bin`, databases, logs, tokens, or browser profile data.
- Update the README, translations, or third-party notices when the change affects them.
- Describe the manual Windows test you ran.

## License

By contributing, you agree that your contribution is licensed under the MIT License in [LICENSE](LICENSE).
