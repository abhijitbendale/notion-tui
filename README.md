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

- Ubuntu or another Debian-based Linux system
- Go installed
- The Notion CLI installed and authenticated
- Access to a Notion workspace

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

## 2) Install Go on Ubuntu

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

## 3) Clone and build

```bash
git clone <your-repo-url>
cd notion-tui
go mod tidy
go build -o notion-tui .
```

## 4) Launch the app

```bash
./notion-tui
```

You can also run it directly with Go during development:

```bash
go run .
```

## Keyboard shortcuts

- `↑` / `↓` or `j` / `k`: move through the page tree
- `Enter`: open a page
- `Tab`: switch between panes
- `/`: filter pages
- `e`: edit the selected page in your editor
- `r`: refresh the Notion tree
- `?`: help overlay
- `q`: quit

## Notes

- The app uses a local cache to make startup faster on subsequent launches.
- It still depends on the Notion CLI for the actual source of truth and editor flow.

## License

This project is licensed under the MIT License. See the [LICENSE](LICENSE) file for details.
