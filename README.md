# CYRON Manager

Windows desktop download manager built with Go, Wails, SQLite, aria2, yt-dlp, and ffmpeg. The UI ships in Persian RTL by default, with English and light/dark themes.

![Platform: Windows](https://img.shields.io/badge/platform-Windows-0078D4)
![Go 1.26](https://img.shields.io/badge/go-1.26-00ADD8)
![License: MIT](https://img.shields.io/badge/license-MIT-green)

## فارسی

CYRON Manager یک برنامه مدیریت دانلود برای ویندوز است. با Go، Wails و SQLite نوشته شده و رابط کاربری آن به‌طور پیش‌فرض فارسی و راست‌به‌چپ است. رابط انگلیسی و تم روشن و تیره هم در دسترس است.

### امکانات

- دانلود مستقیم HTTP/HTTPS با چند اتصال و امکان ادامه دانلود. aria2 مسئول تورنت و دانلودهایی است که صریحاً با aria2 خواسته می‌شوند.
- دانلود ویدیو و صدا با yt-dlp از یوتیوب و سایت‌های پشتیبانی‌شده.
- تورنت و لینک magnet با aria2، شامل فایل‌های محلی `.torrent`.
- پروکسی اختیاری HTTP یا SOCKS، همراه با نام کاربری، رمز عبور و هدر کوکی.
- صف دانلود با ارسال خودکار، توقف، ادامه، لغو، تلاش مجدد، نمایش سرعت و تاریخچه سرعت.
- ترتیب دستی صف با جابه‌جایی بالا و پایین. صف از همین ترتیب پیروی می‌کند.
- دسته‌بندی ویدیو، صدا، آرشیو، برنامه و فایل‌های دیگر، با تشخیص خودکار و امکان تغییر دستی.
- کشیدن و رها کردن فایل `.torrent` یا آدرس دانلود روی پنجره.
- اعلان ویندوز پس از پایان دانلود.
- اتصال مرورگر برای افزونه Chrome/Edge: دریافت خودکار یا دستی صفحه و لینک، با حالت پرسیدن، دسته‌بندی و ارسال اختیاری کوکی.
- زمان‌بندی دانلود که خارج از بازه تعیین‌شده متوقف و داخل آن ادامه می‌دهد. بازه می‌تواند از نیمه‌شب عبور کند.
- خاموش‌کردن ویندوز پس از پایان همه دانلودها، به‌صورت اختیاری.
- تشخیص آدرس در کلیپ‌بورد و افزودن خودکار به صف، به‌صورت اختیاری.
- جست‌وجوی مخزن GitHub، دانلود ZIP شاخه و فایل‌های Release.
- داده‌ها فقط روی همان دستگاه می‌مانند. پل مرورگر فقط روی localhost گوش می‌دهد. حساب، تله‌متری و سرور مرکزی وجود ندارد.

### دانلود و اجرا

از صفحه Releases فایل `CYRON-Setup-*.exe` را بگیر. نصب‌کننده به‌صورت per-user نصب می‌کند و به Administrator نیاز ندارد. ابزارهای زمان اجرا داخل نصب‌کننده هستند، Shortcut دسکتاپ و Start Menu ساخته می‌شود و در صورت نبود WebView2 نصب‌کننده آن را نصب می‌کند. در اولین اجرا از تنظیمات، پوشه دانلود را انتخاب کن.

### ساخت از سورس

ویندوز ۱۰ یا ۱۱، Go 1.26 یا جدیدتر، Wails v2.17.1 و PowerShell 5.1 یا جدیدتر لازم است:

```powershell
powershell -ExecutionPolicy Bypass -File scripts/fetch-tools.ps1
powershell -ExecutionPolicy Bypass -File scripts/build.ps1
```

خروجی در `build/bin/CYRON.exe` ساخته می‌شود. برای ساخت نصب‌کننده NSIS، پس از نصب NSIS دستور `scripts/build.ps1 -Nsis` را اجرا کن. نصب‌کننده در `build/CYRON-Setup-1.1.0.exe` ساخته می‌شود و ابزارهای runtime، لایسنس‌ها، Shortcutها و uninstaller را داخل خود دارد. بسته افزونه مرورگر هم در `build/CYRON-Browser-Extension-1.1.0.zip` ساخته می‌شود.

### داده و امنیت

- داده برنامه: `%LOCALAPPDATA%\CYRON`
- پایگاه داده SQLite: `%LOCALAPPDATA%\CYRON\downloads.db`
- پوشه پیش‌فرض دانلود: `%USERPROFILE%\Downloads\CYRON`
- پروفایل WebView2: `%LOCALAPPDATA%\CYRON\WebView2`

توکن اختیاری GitHub قبل از ذخیره در SQLite با DPAPI ویندوز رمز می‌شود. اتصال مرورگر توکن ندارد؛ پل با شناسه ثابت افزونه و Origin محدود کار می‌کند و فقط روی `127.0.0.1:17872` گوش می‌دهد. افزونه در `%LOCALAPPDATA%\CYRON\browser-extension` استخراج می‌شود. از تنظیمات پوشه را باز کن و در Chrome یا Edge به‌صورت unpacked بارگذاری کن. اگر پورت را عوض کردی، همان نشانی را در تنظیمات افزونه هم به‌روز کن. کوکی‌های ارسالی با DPAPI رمز می‌شوند و پس از کامل شدن دانلود پاک می‌شوند. راهنمای کامل در `docs/browser-integration.md` است.

توکن GitHub، آدرس خصوصی یا فایل پایگاه داده را در issue عمومی نگذار. برای گزارش آسیب‌پذیری، `SECURITY.md` را بخوان.

### محدودیت‌ها

- فقط ویندوز.
- به‌روزرسانی خودکار ندارد.
- ساخت نصب‌کننده NSIS به نصب NSIS نیاز دارد.
- نصب‌کننده و فایل‌های اجرایی امضای دیجیتال ندارند؛ Windows SmartScreen ممکن است هشدار بدهد.
- افزونه مرورگر به‌صورت unpacked بارگذاری می‌شود و در Chrome Web Store یا Edge Add-ons منتشر نشده.
- پل مرورگر به‌طور پیش‌فرض از پورت `17872` استفاده می‌کند. پورت در تنظیمات قابل تغییر است؛ اگر برنامه دیگری پورت را بگیرد، پل تا آزاد شدن آن خطا می‌دهد.
- FFmpeg حجیم است. با `fetch-tools.ps1 -SkipFFmpeg` می‌توانی از دانلود آن صرف‌نظر کنی، اما ادغام ویدیو و استخراج صدا از کار می‌افتد. برای نگه‌داشتن این حالت، `build.ps1` را با `-NoFetch` اجرا کن.

### مشارکت و لایسنس

قبل از pull request، `CONTRIBUTING.md` را بخوان. مجوز پروژه MIT است. ابزارهای زمان اجرا مجوز خودشان را دارند: aria2 و FFmpeg با GPL و yt-dlp با Unlicense. قبل از بازتوزیع بسته، `THIRD_PARTY_NOTICES.md` را بخوان.

---

The English documentation follows.

## Download

Use the repository's Releases page. `CYRON-Setup-*.exe` is the per-user Windows installer. It bundles the runtime tools, creates Start Menu and desktop shortcuts, registers an uninstaller, and installs WebView2 when it is missing. It does not require Administrator.

On first launch, open Settings and choose a download folder.

## Features

- Direct HTTP/HTTPS downloads use the built-in multi-connection engine with resume. aria2 handles torrents and explicit aria2 requests.
- Video and audio downloads through yt-dlp for YouTube and other supported sites.
- Torrent and magnet links through aria2, including local `.torrent` files.
- Proxy profiles for downloads: save multiple HTTP, HTTPS, or SOCKS5 proxies, pick a default, add fallbacks, and override the proxy per download. With optional bypass rules and a built-in connection test.
- Queue with automatic dispatch, pause/resume/cancel, auto-retry of failed downloads, progress, speed history, and storage usage.
- Manual queue order with move-up and move-down actions; the dispatcher follows that order.
- Categories for video, audio, archive, program, and other files, with automatic detection and manual override.
- Drag and drop a `.torrent` file or a download URL onto the window.
- Optional Windows notification when a download finishes.
- Browser connection for the bundled Chrome/Edge extension: capture pages and links automatically or send them manually, with an ask mode, category filters, site exclusions, and optional cookie forwarding.
- Download schedule that pauses outside the configured window and resumes inside it. Windows may cross midnight.
- Optional Windows shutdown when all downloads finish.
- Clipboard URL detection with optional automatic queueing.
- GitHub repository search, branch ZIP archives, and release asset downloads.
- Local-only app data. The browser bridge listens on localhost only. No account, telemetry, or central server.

## Requirements

- Windows 10 or 11.
- Go 1.26 or newer.
- Wails v2.17.1 or newer.
- PowerShell 5.1 or newer.
- WebView2 Runtime, which is included in current Windows builds.

## Build from source

Clone the repository, then run:

```powershell
powershell -ExecutionPolicy Bypass -File scripts/fetch-tools.ps1
powershell -ExecutionPolicy Bypass -File scripts/build.ps1
```

`fetch-tools.ps1` downloads aria2, yt-dlp, ffmpeg, ffprobe, and their license files into `bin/`. `build.ps1` runs `wails build`, copies the tools next to the app, creates the browser extension package, and writes:

```text
build/bin/CYRON.exe
build/bin/bin/aria2c.exe
build/bin/bin/yt-dlp.exe
build/bin/bin/ffmpeg.exe
build/bin/bin/ffprobe.exe
build/bin/bin/licenses/
```

The browser extension package is written to `build/CYRON-Browser-Extension-1.1.0.zip`. Extract it before loading the folder as an unpacked extension.

To create a per-user NSIS installer, install NSIS and run:

```powershell
powershell -ExecutionPolicy Bypass -File scripts/build.ps1 -Nsis
```

The installer is written to `build/CYRON-Setup-1.1.0.exe`. It contains the runtime tools, the WebView2 bootstrapper, Start Menu and desktop shortcuts, and an uninstaller. The installer is optional and is not built by default.

## Development

```powershell
wails dev
```

The frontend is static HTML, CSS, and JavaScript in `frontend/dist`. There is no Node build step. Wails generates `frontend/wailsjs` when bindings are needed; that folder is ignored by Git.

Run the test suite on Windows:

```powershell
go test ./...
go vet ./...
node --test browser-extension/tests/lib.test.js
```

The full suite depends on the Windows-only package `internal/secure`, so it does not run as-is on Linux.

## Project layout

| Path | Purpose |
| --- | --- |
| `app.go` | Wails application API and window lifecycle |
| `main.go` | Wails entry point and embedded frontend assets |
| `internal/` | Download engines, store, proxy manager, bridge, aria2, tools, and platform helpers |
| `frontend/dist/` | Static UI, translations, and styles |
| `browser-extension/` | Chrome/Edge browser connection extension |
| `scripts/` | Tool download and build scripts |
| `build/` | Wails icon, manifest, generated output, and extension signing key |
| `bin/` | Local runtime tools, ignored by Git |

## Data and security

- App data: `%LOCALAPPDATA%\CYRON`
- SQLite database: `%LOCALAPPDATA%\CYRON\downloads.db`
- Default downloads: `%USERPROFILE%\Downloads\CYRON`
- WebView2 profile: `%LOCALAPPDATA%\CYRON\WebView2`

The optional GitHub token is encrypted with Windows DPAPI before it is written to SQLite. Proxy profile passwords are encrypted the same way and never reach the UI. The browser connection has no pairing token: the bridge uses a fixed extension ID, validates the extension origin, and listens on loopback only. Forwarded browser cookies are also encrypted with DPAPI and are cleared when the download completes.

For proxy setup and troubleshooting, see [docs/proxy.md](docs/proxy.md).

The bundled extension is extracted to `%LOCALAPPDATA%\CYRON\browser-extension`. Open it from Settings, load that folder as an unpacked extension in Chrome or Edge, and keep the address and port in the extension options equal to the app settings. See [docs/browser-integration.md](docs/browser-integration.md) for the full setup and troubleshooting guide.

Do not paste tokens, private URLs, or database files into issues. See [SECURITY.md](SECURITY.md) for vulnerability reports.

## Third-party tools

Runtime binaries are not stored in this repository. `scripts/fetch-tools.ps1` downloads them and keeps their license files in `bin/licenses`.

- aria2: GPL-2.0
- ffmpeg: build-dependent GPL license, included with the downloaded build
- yt-dlp: Unlicense

See [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md) before redistributing a bundle that includes the binaries.

## Limitations

- Windows only.
- No automatic updates.
- NSIS installer generation requires NSIS.
- The installer and executables are not code-signed, so Windows SmartScreen may warn.
- The browser extension is loaded unpacked. It is not published to the Chrome Web Store or Edge Add-ons.
- The browser bridge uses local port `17872` by default. You can change it in Settings. If another process holds the port, the bridge reports an error until the port is free.
- FFmpeg is large. `fetch-tools.ps1 -SkipFFmpeg` skips it, but video merging and audio extraction will not be available. Pass `-NoFetch` to `build.ps1` to keep it skipped.
- Direct HTTP(S) files use the built-in engine by default. It uses multiple connections when the server supports byte ranges and resumes from its `.part.multi` state. Servers without range support fall back to a single connection. aria2 handles torrents and explicit aria2 requests.

## Contributing

Read [CONTRIBUTING.md](CONTRIBUTING.md) before opening a pull request. Bug reports and feature requests use the GitHub issue templates.

## License

MIT. See [LICENSE](LICENSE).
