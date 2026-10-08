# notion-tui

A simple Go + Bubble Tea terminal UI for browsing and editing Notion content from the terminal.

This app relies on the official Notion CLI (`ntn`) for authentication and data access, then renders the page tree and content in a lightweight TUI.

## Features

- Sidebar page tree
- Preview pane for page content
- Cached startup for faster reloads
- Refresh support while keeping the app responsive
- Edit a page in your editor via the Notion CLI workflow

## Requirements

- Linux (x86_64 or ARM64)
- The Notion CLI installed and authenticated
- Access to a Notion workspace

Go is only required when building from source. Release binaries do not require Go.

## 1) Set up the Notion CLI first

Before running the TUI, make sure the `ntn` command is installed and working.

1. Install the official Notion CLI using its installation instructions for your platform.
2. Verify it is on your `PATH`:

```bash
ntn --version
```

3. Log in:

```bash
ntn login
```

4. Confirm the CLI works before launching the app:

```bash
ntn api v1/search
```

If the command fails, fix the CLI installation first. This app expects `ntn` to be available on your shell path.

`notion-tui` also checks for an active `ntn` authentication token before
starting. If the app exits with an authentication error, run:

```bash
ntn login
ntn auth token >/dev/null
```

The second command verifies credentials without printing the token. If you use
a token directly instead of the CLI login flow, set `NOTION_API_TOKEN` in the
same shell used to launch `notion-tui`.

## 2) Install from a release

The Linux installer detects x86_64 and ARM64, verifies the downloaded checksum,
and installs to `/usr/local/bin` when writable. Otherwise it uses
`~/.local/bin`.

First install and authenticate the Notion CLI as described above, then run:

```bash
curl -fsSL https://raw.githubusercontent.com/abhijitbendale/notion-tui/main/install.sh | sh
```

To install a specific release or choose an installation directory:

```bash
curl -fsSL https://raw.githubusercontent.com/abhijitbendale/notion-tui/main/install.sh \
  | sh -s -- --version v0.1.0 --prefix "$HOME/.local/bin"
```

The installer refuses to continue if `ntn` is not on `PATH`. To install the
binary before setting up `ntn`, use `--skip-ntn-check`; the application itself
still requires `ntn` when it starts.

### Ubuntu/Debian

Download the `.deb` file from the GitHub release page and install it with:

```bash
sudo apt install ./notion-tui_*_amd64.deb
```

The `.deb` package installs the binary only. The `ntn` CLI must be installed
separately.

### Fedora/Arch and other distributions

Use the architecture-matched `.tar.gz` release:

```bash
tar -xzf notion-tui_Linux_x86_64.tar.gz
sudo install -m 0755 notion-tui /usr/local/bin/notion-tui
```

The installer script works on distributions that provide `curl`, `tar`, and
`sha256sum`.

## 3) Install Go from source

If Go is not already installed, install it with the official tarball.

```bash
sudo apt update
sudo apt install -y curl ca-certificates

curl -fsSL https://go.dev/dl/go1.27.1.linux-amd64.tar.gz -o /tmp/go.tar.gz
sudo rm -rf /usr/local/go
sudo tar -C /usr/local -xzf /tmp/go.tar.gz

echo 'export PATH=$PATH:/usr/local/go/bin' >> ~/.bashrc
source ~/.bashrc
go version
```

If `go version` prints a Go version, the toolchain is ready.

## 4) Clone and build

```bash
git clone <your-repo-url>
cd notion-tui
go mod tidy
go build -o notion-tui .
```

## 5) Launch the app

```bash
./notion-tui
```

You can also run it directly with Go during development:

```bash
go run .
```

## Keyboard shortcuts

- `↑` / `↓` or `j` / `k`: move through the page tree or database rows
- `Home` / `End` or `g` / `G`: jump to the beginning or end
- `Page Up` / `Page Down`: move by a screenful
- `Enter`: open a page
- `Tab`: switch between panes
- `/`: filter pages; use arrow keys to browse matches and Enter to open the selected page
- `e`: edit the selected page in your editor
- `o`: open the selected page in a browser
- `y`: copy the selected page ID
- `u`: copy the selected page URL
- `r`: refresh the Notion tree
- `R`: retry the active page or database after an error
- `w`: toggle full-width content view
- `?`: help overlay
- `q`: quit

## Linux browser and clipboard integration

The `o` shortcut opens a page using `xdg-open`. In WSL, `wslview` is preferred
when available so the page opens in the Windows browser. The `y` and `u`
shortcuts support `wl-copy`, `xclip`, `xsel`, and `pbcopy`; WSL also supports
`clip.exe`.

Install the integration tools appropriate for your environment if these
shortcuts report that no tool was found. The core browsing and editing
features do not depend on browser or clipboard integration.

## Notes

- The app uses a local cache to make startup faster on subsequent launches.
- Cached content is shown immediately while a refresh runs in the background;
  the status bar shows the cache age when applicable.
- Refreshes cancel older in-progress loads, so stale results cannot replace
  newer data.
- Database views render only the visible rows while navigating, which keeps
  large databases responsive.
- It still depends on the Notion CLI for the actual source of truth and editor flow.

## License

This project is licensed under the MIT License. See the [LICENSE](LICENSE) file for details.

## Release and platform support

Tagged releases are built by GitHub Actions with GoReleaser. Current release
artifacts target Linux x86_64 and ARM64 and include:

- Checksummed `.tar.gz` archives
- `.deb` packages
- Source-independent release binaries that do not require Go

macOS and native Windows packages are not currently published or tested. They
can be added after testing on representative machines.
