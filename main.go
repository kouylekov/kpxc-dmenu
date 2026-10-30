package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type options struct {
	menu         string
	clip         string
	kpxcBin      string
	showGroup    bool
	showOTP      bool
	clearSeconds int
	refreshTTL   time.Duration
	cachePath    string
	prompt       string
	lines        int
}

func main() {
	opts := parseFlags()

	// Resolve tool binaries; these all have :0/wayland available.
	menuBin := mustExecPath(opts.menu)
	clipBin := mustExecPath(opts.clip)
	kpxcBin := mustExecPath(opts.kpxcBin)

	// Load cache; refresh in the background if stale so the menu opens fast.
	items := loadOrFetch(opts.cachePath, opts.refreshTTL, kpxcBin)

	if len(items) == 0 {
		fmt.Fprintln(os.Stderr, "kpxc-dmenu: no credentials found")
		os.Exit(1)
	}

	// Build the unique, disambiguated labels shown in the menu. Items with an
	// OTP get a dedicated entry so the code can be copied on demand.
	labels, entries := buildEntries(items, opts.showGroup, opts.showOTP)

	selected := showMenu(menuBin, opts.prompt, opts.lines, labels)
	if selected == "" {
		os.Exit(0)
	}

	e, ok := entries[selected]
	if !ok {
		fmt.Fprintln(os.Stderr, "kpxc-dmenu: unknown selection")
		os.Exit(1)
	}

	var secret string
	var what string
	var err error
	if e.kind == "otp" {
		secret, err = fetchTOTP(kpxcBin, e.item)
		what = "one-time code"
	} else {
		secret, err = fetchPassword(kpxcBin, e.item)
		what = "password"
	}
	if err != nil {
		notify("kpxc-dmenu", "Failed to fetch: "+err.Error())
		fmt.Fprintln(os.Stderr, err)
		logErr(opts.cachePath, err)
		os.Exit(1)
	}

	copyToClipboard(clipBin, secret)
	notify("kpxc-dmenu", fmt.Sprintf("%s for %q copied to clipboard", what, e.item.Title))

	if opts.clearSeconds > 0 {
		// Strictly a goroutine, a freshly started wl-copy serves the selection
		// from its own process, so we must stay alive until the clear runs;
		// otherwise this process exits and the goroutine (and its copy) never
		// happens.
		cleared := make(chan struct{})
		go func() {
			defer close(cleared)
			time.Sleep(time.Duration(opts.clearSeconds) * time.Second)
			copyToClipboard(clipBin, "")
		}()
		<-cleared
	}
}

func parseFlags() options {
	var opts options
	flag.StringVar(&opts.menu, "menu", "fuzzel", "menu program")
	flag.StringVar(&opts.clip, "clip", "wl-copy", "clipboard tool")
	flag.StringVar(&opts.kpxcBin, "kpxc", "kpxc", "path to the kpxc (keepassxc-cli) wrapper")
	flag.BoolVar(&opts.showGroup, "show-group", true, "show the group name in the menu")
	flag.BoolVar(&opts.showOTP, "show-otp", true, "show OTP entries for items that support them")
	flag.IntVar(&opts.clearSeconds, "clear-seconds", 20, "auto-clear clipboard after N seconds (0 = never)")
	flag.DurationVar(&opts.refreshTTL, "refresh-ttl", 30*time.Second, "cache freshness window")
	flag.StringVar(&opts.cachePath, "cache", defaultCachePath(), "cache file for credential names")
	flag.StringVar(&opts.prompt, "prompt", "", "menu prompt")
	flag.IntVar(&opts.lines, "lines", 0, "number of menu lines to show (0 = automatic)")
	flag.Parse()

	return opts
}

func defaultCachePath() string {
	if x := os.Getenv("XDG_CACHE_HOME"); x != "" {
		return filepath.Join(x, "keepassxc-dmenu", "items.json")
	}
	return filepath.Join(os.Getenv("HOME"), ".cache", "keepassxc-dmenu", "items.json")
}

// loadOrFetch returns cache contents, refreshing in the background when stale.
// On first run (no cache) it refreshes synchronously so the menu isn't empty.
func loadOrFetch(cachePath string, ttl time.Duration, kpxcBin string) []Item {
	c, err := loadCache(cachePath)
	haveCache := err == nil

	if haveCache && !c.stale(ttl) {
		return c.Items
	}

	if haveCache {
		// Stale: serve the old list now, refetch in the background for next time.
		go func() {
			if items, err := listItems(kpxcBin); err == nil {
				_ = saveCache(cachePath, items)
			}
		}()
		return c.Items
	}

	// First run: block on a fresh fetch.
	if items, err := listItems(kpxcBin); err == nil {
		_ = saveCache(cachePath, items)
		return items
	}
	return nil
}

// entry pairs a menu label with the credential and the action to perform on
// selection. kind is either "password" or "otp".
type entry struct {
	label string
	item  Item
	kind  string
}

// buildEntries produces unique display labels and a lookup back to the
// credential + action. Titles are disambiguated across groups and actions.
func buildEntries(items []Item, showGroup, showOTP bool) ([]string, map[string]*entry) {
	labels := make([]string, 0, len(items))
	entries := make(map[string]*entry, len(items))
	seen := map[string]int{}

	// Seconds left in the current TOTP window, snapshotted as the menu opens.
	// Standard TOTP uses a 30s period aligned to the Unix epoch; fuzzel labels
	// don't update after the menu is drawn, so this is a static hint.
	otpLeft := totpSecondsLeft(30)

	add := func(it Item, kind string) {
		label := fmt.Sprintf("%s  [%s]", it.Title, it.Group)
		if kind == "otp" {
			label = fmt.Sprintf("%s · OTP · %ds", it.Title, otpLeft)
			if showGroup {
				label = fmt.Sprintf("%s · OTP · %ds  [%s]", it.Title, otpLeft, it.Group)
			}
		} else if !showGroup {
			label = it.Title
		}
		n := seen[label]
		seen[label]++
		if n > 0 {
			label = fmt.Sprintf("%s (%d)", label, n+1)
		}
		labels = append(labels, label)
		entries[label] = &entry{label: label, item: it, kind: kind}
	}

	for _, it := range items {
		// Show the password entry unless we positively know there is none
		// (e.g. an OTP-only item). Unknown cases emit it and let item view try.
		if it.HasPassword || !it.HasOTP {
			add(it, "password")
		}
		if showOTP && it.HasOTP {
			add(it, "otp")
		}
	}
	return labels, entries
}

// totpSecondsLeft returns how many seconds remain in the current TOTP window
// for the given period (e.g. 30), based on the wall clock at call time.
func totpSecondsLeft(period int64) int64 {
	if period <= 0 {
		period = 30
	}
	return period - (time.Now().Unix() % period)
}

// showMenu pipes lines into the menu program and returns the chosen line.
func showMenu(cmdPath, prompt string, lines int, labels []string) string {
	args := []string{"--dmenu", "--prompt", prompt}
	if lines > 0 {
		args = append(args, "--lines", fmt.Sprintf("%d", lines))
	}
	cmd := exec.Command(cmdPath, args...)
	cmd.Stdin = bytes.NewBufferString(strings.Join(labels, "\n") + "\n")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func copyToClipboard(clip string, text string) {
	cmd := exec.Command(clip)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		fmt.Fprintln(os.Stderr, "clipboard:", err)
		return
	}
	if err := cmd.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "clipboard:", err)
		return
	}
	// Write the payload synchronously and close stdin before returning. Go's
	// exec feeds stdin on a background goroutine that is killed the moment this
	// process exits — under a short-lived Hyprland keybind that race truncates
	// the data, so wl-copy ends up holding nothing. Pushing the bytes ourselves
	// (into the pipe / wl-copy's buffer) before exiting makes the copy reliable.
	if _, err := stdin.Write([]byte(text)); err != nil {
		cmd.Process.Kill()
		fmt.Fprintln(os.Stderr, "clipboard:", err)
		return
	}
	if err := stdin.Close(); err != nil {
		cmd.Process.Kill()
		fmt.Fprintln(os.Stderr, "clipboard:", err)
		return
	}
	// wl-copy stays alive serving the wl_selection; detach it so it survives
	// our process exiting instead of blocking on Wait().
	_ = cmd.Process.Release()
}

func notify(summary, body string) {
	if p, err := exec.LookPath("notify-send"); err == nil {
		_ = exec.Command(p, summary, body).Run()
	}
}

// logErr appends an error to a plain-text log next to the cache, so details
// that get truncated in a desktop notification (long kpxc stderr) are
// still available to read in full.
func logErr(cachePath string, err error) {
	f, ferr := os.OpenFile(filepath.Join(filepath.Dir(cachePath), "error.log"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if ferr != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s: %v\n", time.Now().Format(time.RFC3339), err)
}
