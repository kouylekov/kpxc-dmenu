# pass-cli-dmenu

A fast [fuzzel](https://codeberg.org/dnkl/fuzzel)/dmenu launcher for
[Proton Pass](https://proton.me/pass) via the official
[`pass-cli`](https://proton.me/support/pass-cli) tool. Pick a credential from a
menu and its password (or a fresh TOTP code) is copied to the Wayland clipboard,
then cleared automatically after a short delay.

## Features

- Lists logins across all your Proton Pass vaults.
- Copies passwords, and generates one-time codes for items with OTP configured.
- Shows the seconds remaining in the current TOTP window on OTP entries.
- Auto-clears the clipboard after N seconds (default 20).
- Caches credential *names only* (never secrets) so the menu opens instantly and
  refreshes in the background.
- Desktop notifications on copy and on failure, with a full error log for
  details that get truncated in notifications.

## Requirements

- [`pass-cli`](https://proton.me/support/pass-cli), logged in (`pass-cli login`)
- A menu program — [`fuzzel`](https://codeberg.org/dnkl/fuzzel) by default
  (anything that speaks `--dmenu` works)
- `wl-copy` (from `wl-clipboard`) for the clipboard
- `notify-send` (optional) for notifications
- Go 1.26+ to build

## Build

```sh
go build -o pass-cli-dmenu .
```

## Usage

```sh
./pass-cli-dmenu
```

### Hyprland keybind

```lua
hl.bind(mainMod .. " + U", hl.dsp.exec_cmd("/path/to/pass-cli-dmenu"))
```

> **Note:** launchers such as a Hyprland keybind start with a minimal `PATH`
> that usually omits `~/.local/bin`. The tool probes the common per-user bin
> directories (`~/.local/bin`, `~/.nix-profile/bin`, `~/bin`) so `pass-cli` is
> still found there; if your `pass-cli` lives elsewhere, pass its absolute path
> with `-pass`.

## Options

| Flag             | Default          | Description                                             |
| ---------------- | ---------------- | ------------------------------------------------------- |
| `-menu`          | `fuzzel`         | Menu program (must support `--dmenu`)                   |
| `-clip`          | `wl-copy`        | Clipboard tool                                          |
| `-pass`          | `pass-cli`       | Path to the `pass-cli` binary                           |
| `-types`         | `login`          | Comma-separated item types to show (empty = all)        |
| `-show-vault`    | `true`           | Show the vault name in the menu                         |
| `-show-otp`      | `true`           | Show OTP entries for items that support them            |
| `-clear-seconds` | `20`             | Auto-clear the clipboard after N seconds (`0` = never)  |
| `-refresh-ttl`   | `30s`            | Cache freshness window                                  |
| `-cache`         | XDG cache path   | Cache file for credential names                         |
| `-prompt`        | *(empty)*        | Menu prompt text                                        |
| `-lines`         | `0`              | Number of menu lines to show (`0` = automatic)          |

## Security notes

- Only credential *metadata* (titles, vault names, item IDs) is cached — never
  passwords or OTP secrets. Cache and log files are written with `0600`
  permissions.
- Passwords/codes are fetched on demand from `pass-cli` at selection time and
  wiped from the clipboard after `-clear-seconds`.
