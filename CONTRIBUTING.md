# Contributing to Orbi-VPN

Thanks for your interest in improving Orbi-VPN. Issues and pull requests are
welcome.

## Project layout

```
cli/      server + client + profile generator (one binary: orbi)
gui/      desktop client (Fyne)
scripts/  build & install helpers
```

## Development setup

- Go 1.22+.
- For the desktop GUI you also need a C toolchain (macOS:
  `xcode-select --install`; Windows: mingw-w64 / TDM-GCC on `PATH`).

Build and run locally:

```bash
# server + CLI
cd cli && go build -o orbi . && cd ..

# desktop app
cd gui && go mod tidy && go build -o orbi-gui . && cd ..
```

## Submitting changes

1. Fork the repo and create a branch: `git checkout -b my-change`.
2. Keep changes focused; match the existing code style (`gofmt`).
3. Build the affected module before opening a PR.
4. Describe what changed and why in the PR. Link any related issue.

## Reporting bugs

Open an issue with:
- what you did, what you expected, what happened;
- your OS and version;
- relevant logs (the app's Log pane, or `journalctl -u orbi` on the server).

Please do not include secrets (tokens, `.orb` profiles, `peers.txt`) in issues.

## Scope / responsible use

Orbi-VPN is for circumventing censorship and running your own private VPN. Keep
contributions aligned with that purpose and with applicable law.
