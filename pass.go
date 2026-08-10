package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type vaultList struct {
	Vaults []struct {
		Name    string `json:"name"`
		ShareID string `json:"share_id"`
	} `json:"vaults"`
}

type itemList struct {
	Items []struct {
		ID      string          `json:"id"`
		ShareID string          `json:"share_id"`
		VaultID string          `json:"vault_id"`
		Content itemContent     `json:"content"`
		State   string          `json:"state"`
		Raw     json.RawMessage `json:"-"`
	} `json:"items"`
}

// itemContent mirrors the "content" object produced by --show-secrets. The
// concrete type (login, note, etc.) is the single key of its inner "content".
type itemContent struct {
	Title   string          `json:"title"`
	Content json.RawMessage `json:"content"`
}

// passJSON runs a pass-cli subcommand and parses its JSON stdout.
func passJSON(passBin string, out interface{}, args ...string) error {
	cmd := exec.Command(passBin, args...)
	raw, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return fmt.Errorf("pass-cli %v: %s", args, strings.TrimSpace(string(ee.Stderr)))
		}
		return err
	}
	return json.Unmarshal(raw, out)
}

// listItems lists logins across all official vaults.
func listItems(passBin, typeFilter string) ([]Item, error) {
	var allowed map[string]bool
	if typeFilter != "" {
		allowed = map[string]bool{}
		for _, t := range strings.Split(typeFilter, ",") {
			allowed[strings.ToLower(strings.TrimSpace(t))] = true
		}
	}

	var vl vaultList
	if err := passJSON(passBin, &vl, "vault", "list", "--output", "json"); err != nil {
		return nil, fmt.Errorf("listing vaults: %w", err)
	}

	var items []Item
	for _, v := range vl.Vaults {
		var il itemList
		// --show-secrets surfaces the totp_uri (among other content) so we can
		// detect OTP availability in one call per vault. We only keep a boolean;
		// the secrets themselves are never cached.
		if err := passJSON(passBin, &il, "item", "list", "--vault-name="+v.Name, "--output", "json", "--show-secrets"); err != nil {
			return nil, fmt.Errorf("listing items in %q: %w", v.Name, err)
		}
		for _, it := range il.Items {
			if allowed != nil && !allowed[strings.ToLower(itemKind(it.Content.Content))] {
				continue
			}
			items = append(items, Item{
				Title:       it.Content.Title,
				Vault:       v.Name,
				ItemID:      it.ID,
				ShareID:     it.ShareID,
				HasOTP:      contentHasValue(it.Content.Content, "totp_uri"),
				HasPassword: contentHasValue(it.Content.Content, "password"),
			})
		}
	}
	return items, nil
}

// itemKind reports the item type from the single key of the inner content
// object (e.g. "Login", "Note", "CreditCard").
func itemKind(raw []byte) string {
	var inner map[string]json.RawMessage
	if err := json.Unmarshal(raw, &inner); err != nil {
		return ""
	}
	for key := range inner {
		return key
	}
	return ""
}

// contentHasValue reports whether raw item content carries a non-empty value
// for the given JSON key.
func contentHasValue(raw []byte, key string) bool {
	needle := `"` + key + `"`
	idx := bytes.Index(raw, []byte(needle))
	if idx < 0 {
		return false
	}
	tail := raw[idx+len(needle):]
	// Skip whitespace and a colon, then check the first value isn't an empty string.
	tail = bytes.TrimLeft(tail, " \t\n\r: ")
	return !bytes.HasPrefix(tail, []byte(`""`))
}

// fetchPassword retrieves the password for an item. It tries several common
// field names, falling back to the whole item's "password" location if needed.
func fetchPassword(passBin string, it Item) (string, error) {
	var lastErr error
	for _, field := range []string{"password", "Password"} {
		args := []string{
			"item", "view",
			"--share-id=" + it.ShareID,
			"--item-id=" + it.ItemID,
			"--field=" + field,
		}
		out, err := runPass(passBin, args...)
		if err != nil {
			lastErr = err
			continue
		}
		if v := strings.TrimSpace(string(out)); v != "" {
			return v, nil
		}
	}
	if lastErr != nil {
		// Surface the real pass-cli failure (e.g. missing decryption key /
		// agent socket) instead of a misleading "field not found".
		return "", fmt.Errorf("fetching password for %q: %w", it.Title, lastErr)
	}
	return "", fmt.Errorf("no password value found for %q", it.Title)
}

// runPass runs pass-cli and returns stdout, or the combined stderr/stdout on
// an error. It periodically requires the local encryption key (ssh/gpg agent).
func runPass(passBin string, args ...string) ([]byte, error) {
	cmd := exec.Command(passBin, args...)
	var buf bytes.Buffer
	cmd.Stderr = &buf
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(buf.String())
		if msg == "" {
			msg = strings.TrimSpace(string(out))
		}
		return nil, errors.New(msg)
	}
	return out, nil
}

type totpOut struct {
	Totp string `json:"totp"`
}

// fetchTOTP generates the current time-based one-time passcode for an item.
func fetchTOTP(passBin string, it Item) (string, error) {
	var to totpOut
	if err := passJSON(passBin, &to, "item", "totp", "--share-id="+it.ShareID, "--item-id="+it.ItemID, "--output", "json"); err != nil {
		return "", fmt.Errorf("generating totp for %q: %w", it.Title, err)
	}
	code := strings.TrimSpace(to.Totp)
	if code == "" {
		return "", fmt.Errorf("no totp code for %q", it.Title)
	}
	return code, nil
}

// mustExecPath returns the absolute path of a binary, resolving bare names via PATH.
func mustExecPath(bin string) string {
	if strings.ContainsRune(bin, os.PathSeparator) {
		return bin
	}
	if p, err := exec.LookPath(bin); err == nil {
		return p
	}
	// Launchers like a Hyprland keybind start the process with a minimal PATH
	// that omits per-user bin dirs (e.g. ~/.local/bin), so a bare name that
	// resolves fine in an interactive shell is "not found" here — the failure
	// mode is a silent, empty exec error when we later try to run pass-cli.
	// Probe the usual per-user locations before giving up on the bare name.
	if home := os.Getenv("HOME"); home != "" {
		for _, dir := range []string{
			filepath.Join(home, ".local", "bin"),
			filepath.Join(home, ".nix-profile", "bin"),
			filepath.Join(home, "bin"),
		} {
			cand := filepath.Join(dir, bin)
			if fi, err := os.Stat(cand); err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
				return cand
			}
		}
	}
	return bin
}
