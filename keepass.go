package main

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// KeePassXC XML export shapes. We only read the group tree and the entry's
// String key/value pairs; the export's secret values are inspected for
// presence only and never cached.
type kpFile struct {
	Root kpRoot `xml:"Root"`
}

type kpRoot struct {
	Group kpGroup `xml:"Group"`
}

type kpGroup struct {
	Name    string    `xml:"Name"`
	Groups  []kpGroup `xml:"Group"`
	Entries []kpEntry `xml:"Entry"`
}

type kpEntry struct {
	Strings []kpString `xml:"String"`
}

type kpString struct {
	Key   string `xml:"Key"`
	Value string `xml:"Value"`
}

// listItems exports the database as XML (one unlock) and flattens it into
// credential metadata. The kpxc wrapper supplies the master password and the
// database path, so no secret ever passes through this process's arguments.
// groups is a comma-separated allow-list of top-level groups (empty = all).
func listItems(kpxc, groups string) ([]Item, error) {
	raw, err := runKPXc(kpxc, "export", "-f", "xml")
	if err != nil {
		return nil, fmt.Errorf("exporting database: %w", err)
	}

	var f kpFile
	if err := xml.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("parsing database export: %w", err)
	}

	var items []Item
	collectEntries(f.Root.Group, nil, &items)
	return filterGroups(items, groups), nil
}

// filterGroups keeps only entries whose group is, or is nested under, one of
// the comma-separated group names in spec. An empty spec keeps everything.
func filterGroups(items []Item, spec string) []Item {
	var prefixes []string
	for _, g := range strings.Split(spec, ",") {
		if g = strings.TrimSpace(g); g != "" {
			prefixes = append(prefixes, g)
		}
	}
	if len(prefixes) == 0 {
		return items
	}

	var out []Item
	for _, it := range items {
		for _, p := range prefixes {
			if it.Group == p || strings.HasPrefix(it.Group, p+"/") {
				out = append(out, it)
				break
			}
		}
	}
	return out
}

// collectEntries walks the group tree, building each entry's full path. The
// synthetic top-level "Root" group and the recycle bin are skipped.
func collectEntries(g kpGroup, parents []string, out *[]Item) {
	path := parents
	if g.Name != "Root" && g.Name != "" {
		path = append(append([]string{}, parents...), g.Name)
	}

	for _, e := range g.Entries {
		title, hasPassword, hasOTP := entryInfo(e)
		if title == "" {
			continue
		}
		group := strings.Join(path, "/")
		entryPath := title
		if group != "" {
			entryPath = group + "/" + title
		}
		*out = append(*out, Item{
			Title:       title,
			Group:       group,
			Path:        entryPath,
			HasOTP:      hasOTP,
			HasPassword: hasPassword,
		})
	}

	for _, cg := range g.Groups {
		if cg.Name == "Recycle Bin" {
			continue
		}
		collectEntries(cg, path, out)
	}
}

// entryInfo pulls the display title and whether the entry carries a password
// and/or an OTP secret.
func entryInfo(e kpEntry) (title string, hasPassword, hasOTP bool) {
	for _, s := range e.Strings {
		switch strings.ToLower(s.Key) {
		case "title":
			title = s.Value
		case "password":
			if s.Value != "" {
				hasPassword = true
			}
		case "otp":
			if s.Value != "" {
				hasOTP = true
			}
		}
	}
	return title, hasPassword, hasOTP
}

// fetchPassword retrieves the entry's password in clear text.
func fetchPassword(kpxc string, it Item) (string, error) {
	out, err := runKPXc(kpxc, "show", "-s", "-a", "Password", it.Path)
	if err != nil {
		return "", fmt.Errorf("fetching password for %q: %w", it.Title, err)
	}
	v := strings.TrimSpace(string(out))
	if v == "" {
		return "", fmt.Errorf("no password value found for %q", it.Title)
	}
	return v, nil
}

// fetchTOTP generates the entry's current time-based one-time code.
func fetchTOTP(kpxc string, it Item) (string, error) {
	out, err := runKPXc(kpxc, "show", "-t", it.Path)
	if err != nil {
		return "", fmt.Errorf("generating totp for %q: %w", it.Title, err)
	}
	code := strings.TrimSpace(string(out))
	if code == "" {
		return "", fmt.Errorf("no totp code for %q", it.Title)
	}
	return code, nil
}

// runKPXc runs the kpxc wrapper and returns stdout, or its stderr on failure.
func runKPXc(kpxc string, args ...string) ([]byte, error) {
	cmd := exec.Command(kpxc, args...)
	var buf bytes.Buffer
	cmd.Stderr = &buf
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(buf.String())
		if msg == "" {
			msg = strings.TrimSpace(string(out))
		}
		if msg == "" {
			msg = err.Error()
		}
		return nil, errors.New(msg)
	}
	return out, nil
}

// mustExecPath returns the absolute path of a binary, resolving bare names via PATH.
func mustExecPath(bin string) string {
	if strings.ContainsRune(bin, os.PathSeparator) {
		return bin
	}
	if p, err := exec.LookPath(bin); err == nil {
		return p
	}
	// Launchers like a compositor keybind start the process with a minimal PATH
	// that omits per-user bin dirs, so a bare name that resolves fine in an
	// interactive shell is "not found" here — the failure mode is a silent,
	// empty exec error when we later try to run kpxc. Probe the usual locations
	// before giving up on the bare name.
	if home := os.Getenv("HOME"); home != "" {
		for _, dir := range []string{
			filepath.Join(home, "nixos", "scripts"),
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
