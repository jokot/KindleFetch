# KindleFetch for PC

KindleFetch for Windows and macOS. It works like the Kindle version: search
in the terminal, download, and, if your Kindle is plugged in by USB, copy the
book straight into its `documents` folder.

## Install

**Windows** - open PowerShell and run:

```powershell
irm https://raw.githubusercontent.com/jokot/KindleFetch/main/pc/install.ps1 | iex
```

Then start **KindleFetch** from the Start Menu, or type `kindlefetch` in a new
terminal window. No administrator rights are needed.

**macOS** - open Terminal and run:

```sh
curl -fsSL https://raw.githubusercontent.com/jokot/KindleFetch/main/pc/install.sh | sh
```

Then type `kindlefetch` in a new Terminal window, or double-click
**KindleFetch** in your Home > Applications folder. Works on Apple Silicon and
Intel Macs. The same command works on Linux.

Run the install command again at any time to update.

## Use

```
1. Search and download books
2. Filter search results
3. List my books
4. Send books to Kindle (USB)
5. Settings
```

- **Search** uses Z-Library's app API, which is not behind the website's
  browser challenge, and falls back to Libgen. Anna's Archive is not used: its
  search is behind DDoS-Guard for every non-browser client.
- **Downloading from Z-Library** needs a free account; log in under Settings.
  Free accounts have a daily download limit.
- Books are saved to `Downloads/KindleFetch` (changeable in Settings) and named
  `Title - Author.epub`.
- **Copy to Kindle**: plug the Kindle in by USB and unlock it. KindleFetch finds
  it automatically and offers to copy each download, or use menu 4 to copy
  books you already have. Eject the Kindle before unplugging it. Newer Kindles
  that connect over MTP rather than as a drive are not supported; use Amazon's
  Send to Kindle for those.
- **Settings > Network diagnostics** shows which mirrors work from your
  connection.

Settings, including the Z-Library login, are stored in
`%APPDATA%\KindleFetch\config.json` on Windows and
`~/Library/Application Support/KindleFetch/config.json` on macOS.

## Uninstall

Windows (PowerShell):

```powershell
$env:KF_UNINSTALL='1'; irm https://raw.githubusercontent.com/jokot/KindleFetch/main/pc/install.ps1 | iex; Remove-Item Env:KF_UNINSTALL
```

macOS:

```sh
curl -fsSL https://raw.githubusercontent.com/jokot/KindleFetch/main/pc/install.sh | KF_UNINSTALL=1 sh
```

Downloaded books and settings are kept.

## Building

Requires Go. From this folder:

```sh
go test ./...
go build -o kindlefetch .
```

Releases are built by `.github/workflows/pc-release.yml` when a tag starting
with `pc-v` is pushed:

```sh
git tag pc-v1.0.0 && git push origin pc-v1.0.0
```
