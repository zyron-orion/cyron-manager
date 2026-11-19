# Third-party notices

CYRON Manager is licensed under the MIT License. See [LICENSE](LICENSE).

The repository does not store runtime binaries. `scripts/fetch-tools.ps1` downloads them and copies their license files into `bin/licenses`. `scripts/build.ps1` copies those files to `build/bin/bin/licenses`.

## Runtime tools

### aria2

- Version: 1.37.0 Windows 64-bit build
- License: GPL-2.0
- License file in the release: `bin/licenses/aria2-COPYING.txt`
- Source: https://github.com/aria2/aria2

### yt-dlp

- License: Unlicense
- License file in the release: `bin/licenses/yt-dlp-LICENSE.txt`
- Source: https://github.com/yt-dlp/yt-dlp

### FFmpeg

- Build: BtbN FFmpeg-Builds, win64 GPL
- License: GPL, with the exact terms in the downloaded build
- License file in the release: `bin/licenses/ffmpeg-LICENSE.txt`
- Source: https://github.com/BtbN/FFmpeg-Builds

FFmpeg builds include their own dependencies. Review the bundled `LICENSE.txt` before redistributing the full package.

The release archive contains these GPL binaries. Their corresponding source is available from the upstream projects linked above. Keep those source links with any redistribution.

## Go modules

Direct dependencies from `go.mod`:

- Wails v2: MIT
- go-toast v2: Unlicense or MIT
- golang.org/x/sys: BSD-3-Clause
- modernc.org/sqlite: BSD-3-Clause

Transitive modules are listed in `go.mod` and `go.sum`. Their license files are available from the corresponding module repositories.

## Bundled fonts and icons

- Estedad, Inter, and Space Grotesk: SIL Open Font License 1.1. License files are in `frontend/dist/assets/fonts/` in the repository and `licenses/` in the release archive.
- Gentleface icon set: MIT. License file is `frontend/dist/assets/gentleface-LICENSE.txt` in the repository and `licenses/` in the release archive.

## Microsoft WebView2

The app uses the WebView2 Runtime that is installed with Windows or provided by Microsoft. The runtime is not redistributed in this repository or in the release archive.
