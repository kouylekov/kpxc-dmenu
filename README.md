# kpxc-dmenu

A fast [fuzzel](https://codeberg.org/dnkl/fuzzel)/dmenu launcher for
[KeePassXC](https://keepassxc.org/). Pick a credential from a menu and its
password (or a fresh TOTP code) is copied to the Wayland clipboard, then cleared
automatically after a short delay.

The database is unlocked through the [`kpxc`](../../nixos/scripts/kpxc) wrapper,
which pulls the master password from `pass` (GPG) so the menu never prompts.

## Features

- Lists entries from the configured group(s) (default: `Work`).
- Copies passwords, and generates one-time codes for entries with OTP configured.
- Shows the seconds remaining in the current TOTP window on OTP entries.
- Auto-clears the clipboard after N seconds (default 20).
- Caches credential *names only* (never secrets) so the menu opens instantly and
  refreshes in the background.
- Desktop notifications on copy and on failure, with a full error log for
  details that get truncated in notifications.

## Requirements

- [`keepassxc`](https://keepassxc.org/) (`keepassxc-cli` on `PATH`)
- The `kpxc` wrapper (reads the master password from `pass`)
- [`pass`](https://www.passwordstore.org/) + a GPG key, with the master stored at
  `keepassxc/master`
- A menu program — [`fuzzel`](https://codeberg.org/dnkl/fuzzel) by default
  (anything that speaks `--dmenu` works)
- `wl-copy` (from `wl-clipboard`) for the clipboard
- `notify-send` (optional) for notifications
- Go 1.26+ to build

## Build

```sh
go build -o kpxc-dmenu .
```

## Usage

```sh
./kpxc-dmenu
```

### Niri keybind

```kdl
Mod+U { spawn "/home/milen/projects/kpxc-dmenu/kpxc-dmenu"; }
```

> **Note:** launchers such as a compositor keybind start with a minimal `PATH`
> that usually omits `~/nixos/scripts` and `~/.local/bin`. The tool probes those
> common per-user bin directories so `kpxc` is still found; if your wrapper lives
> elsewhere, pass its absolute path with `-kpxc`.

## Options

| Flag             | Default          | Description                                            |
| ---------------- | ---------------- | ------------------------------------------------------ |
| `-menu`          | `fuzzel`         | Menu program (must support `--dmenu`)                  |
| `-clip`          | `wl-copy`        | Clipboard tool                                         |
| `-kpxc`          | `kpxc`           | Path to the `kpxc` (keepassxc-cli) wrapper             |
| `-group`         | `Work`           | Comma-separated groups to include (empty = all)        |
| `-show-group`    | `true`           | Show the group name in the menu                        |
| `-show-otp`      | `true`           | Show OTP entries for items that support them           |
| `-clear-seconds` | `20`             | Auto-clear the clipboard after N seconds (`0` = never) |
| `-refresh-ttl`   | `30s`            | Cache freshness window                                 |
| `-cache`         | XDG cache path   | Cache file for credential names                        |
| `-prompt`        | *(empty)*        | Menu prompt text                                       |
| `-lines`         | `0`              | Number of menu lines to show (`0` = automatic)         |

## Security notes

- Only credential *metadata* (titles, group paths) is cached — never passwords
  or OTP secrets. Cache and log files are written with `0600` permissions.
- Listing runs `keepassxc-cli export -f xml` once (one unlock) and keeps only
  titles/group paths; secrets are never written to the cache.
- Passwords/codes are fetched on demand via `keepassxc-cli` at selection time
  and wiped from the clipboard after `-clear-seconds`.
