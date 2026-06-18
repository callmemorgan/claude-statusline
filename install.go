package main

// ─── Install / Uninstall ─────────────────────────────────────────────
//
// `claude-statusline install` wires the binary into the host tool's settings
// file without reformatting it: the JSON is parse-gated, then the statusLine
// key is spliced in with surgical byte edits — every other byte of the file
// (key order, indentation, number formatting) is preserved. Unparseable
// files are never touched; the exact snippet to paste is printed instead.

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"golang.org/x/term"
)

// ─── JSON splicing primitives ────────────────────────────────────────

// findTopLevelKeySpan locates `"key": value` at depth 1 of a JSON object.
// keyStart is the opening quote of the key, valStart the first byte of the
// value, valEnd one past its last byte.
func findTopLevelKeySpan(raw []byte, key string) (keyStart, valStart, valEnd int, found bool, err error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	tok, err := dec.Token()
	if err != nil {
		return 0, 0, 0, false, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return 0, 0, 0, false, errors.New("settings file is not a JSON object")
	}
	for dec.More() {
		prevOff := int(dec.InputOffset())
		keyTok, err := dec.Token()
		if err != nil {
			return 0, 0, 0, false, err
		}
		k, _ := keyTok.(string)
		var rm json.RawMessage
		if err := dec.Decode(&rm); err != nil {
			return 0, 0, 0, false, err
		}
		if k == key {
			rel := bytes.IndexByte(raw[prevOff:], '"')
			keyStart = prevOff + rel
			valEnd = int(dec.InputOffset())
			valStart = valEnd - len(rm)
			return keyStart, valStart, valEnd, true, nil
		}
	}
	return 0, 0, 0, false, nil
}

// sniffIndent returns the leading whitespace of the first indented line.
func sniffIndent(raw []byte) string {
	for _, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimLeft(line, " \t")
		if trimmed != "" && len(trimmed) < len(line) {
			return line[:len(line)-len(trimmed)]
		}
	}
	return "  "
}

// insertTopLevelKey splices `"key": value` as the first entry of the object.
func insertTopLevelKey(raw []byte, key, value string) ([]byte, error) {
	brace := bytes.IndexByte(raw, '{')
	if brace < 0 {
		return nil, errors.New("no opening brace found")
	}
	rest := bytes.TrimLeft(raw[brace+1:], " \t\r\n")
	empty := len(rest) > 0 && rest[0] == '}'
	indent := sniffIndent(raw)
	if empty {
		return []byte(fmt.Sprintf("{\n%s%q: %s\n}\n", indent, key, value)), nil
	}
	entry := fmt.Sprintf("\n%s%q: %s,", indent, key, value)
	var out bytes.Buffer
	out.Write(raw[:brace+1])
	out.WriteString(entry)
	// Keep the original whitespace between { and the first key so the rest
	// of the file is byte-identical.
	out.Write(raw[brace+1:])
	return out.Bytes(), nil
}

// replaceKeyValue swaps the value of an existing key in place.
func replaceKeyValue(raw []byte, valStart, valEnd int, value string) []byte {
	var out bytes.Buffer
	out.Write(raw[:valStart])
	out.WriteString(value)
	out.Write(raw[valEnd:])
	return out.Bytes()
}

// deleteTopLevelKey removes `"key": value` plus exactly one adjoining comma,
// eating the entry's own line when it sits on one.
func deleteTopLevelKey(raw []byte, key string) ([]byte, bool, error) {
	keyStart, _, valEnd, found, err := findTopLevelKeySpan(raw, key)
	if err != nil || !found {
		return raw, false, err
	}
	isSpace := func(b byte) bool { return b == ' ' || b == '\t' || b == '\r' || b == '\n' }

	// Trailing comma: delete from the start of the entry's line through it.
	i := valEnd
	for i < len(raw) && isSpace(raw[i]) {
		i++
	}
	if i < len(raw) && raw[i] == ',' {
		delStart := keyStart
		for delStart > 0 && (raw[delStart-1] == ' ' || raw[delStart-1] == '\t') {
			delStart--
		}
		if delStart > 0 && raw[delStart-1] == '\n' {
			delStart--
		}
		return append(append([]byte{}, raw[:delStart]...), raw[i+1:]...), true, nil
	}

	// Last key: remove the comma before it instead.
	j := keyStart - 1
	for j >= 0 && isSpace(raw[j]) {
		j--
	}
	if j >= 0 && raw[j] == ',' {
		return append(append([]byte{}, raw[:j]...), raw[valEnd:]...), true, nil
	}

	// Only key.
	return append(append([]byte{}, raw[:keyStart]...), raw[valEnd:]...), true, nil
}

// ─── Install plumbing ────────────────────────────────────────────────

// homeDirOverride redirects home lookups; set only by tests.
var homeDirOverride string

func userHome() string {
	if homeDirOverride != "" {
		return homeDirOverride
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "~"
	}
	return home
}

type installTarget struct {
	name  string
	path  string
	key   string
	value string // JSON value to splice in
}

// resolveCommand returns "claude-statusline" when PATH resolves to this very
// binary, otherwise the absolute path of the running executable (covering
// built-from-source and hand-downloaded installs).
func resolveCommand() string {
	exe, err := os.Executable()
	if err != nil {
		return "claude-statusline"
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	if p, err := exec.LookPath("claude-statusline"); err == nil {
		if rp, err := filepath.EvalSymlinks(p); err == nil && rp == exe {
			return "claude-statusline"
		}
	}
	return exe
}

func resolveTarget(name, explicitPath string) (installTarget, error) {
	cmd := resolveCommand()
	switch name {
	case "claude":
		path := explicitPath
		if path == "" {
			dir := os.Getenv("CLAUDE_CONFIG_DIR")
			if dir == "" {
				dir = filepath.Join(userHome(), ".claude")
			}
			path = filepath.Join(dir, "settings.json")
		}
		return installTarget{
			name:  "claude",
			path:  path,
			key:   "statusLine",
			value: fmt.Sprintf(`{"type": "command", "command": %q}`, cmd),
		}, nil
	case "agy":
		path := explicitPath
		if path == "" {
			candidates := []string{
				filepath.Join(userHome(), ".antigravity", "settings.json"),
				filepath.Join(userHome(), ".config", "antigravity", "settings.json"),
			}
			for _, c := range candidates {
				if _, err := os.Stat(c); err == nil {
					path = c
					break
				}
			}
			if path == "" {
				return installTarget{}, fmt.Errorf("no Antigravity settings file found (looked in %s); pass --settings-path", strings.Join(candidates, ", "))
			}
		}
		return installTarget{
			name:  "agy",
			path:  path,
			key:   "statusline",
			value: fmt.Sprintf("%q", cmd),
		}, nil
	default:
		return installTarget{}, fmt.Errorf("unknown target %q (claude or agy)", name)
	}
}

// backupFile copies path to path.bak, never clobbering an existing backup —
// older backups get a timestamped name instead.
func backupFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	bak := path + ".bak"
	if _, err := os.Stat(bak); err == nil {
		bak = path + ".bak." + time.Now().Format("20060102T1504")
	}
	if err := os.WriteFile(bak, data, 0o600); err != nil {
		return "", err
	}
	return bak, nil
}

func confirm(prompt string, assumeYes bool) bool {
	if assumeYes {
		return true
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Fprintln(os.Stderr, "not a terminal — rerun with --force (or --yes) to proceed")
		return false
	}
	fmt.Printf("%s [y/N] ", prompt)
	var answer string
	fmt.Scanln(&answer) //nolint:errcheck // best-effort prompt read; empty answer defaults to "no"
	answer = strings.ToLower(strings.TrimSpace(answer))
	return answer == "y" || answer == "yes"
}

func manualSnippet(t installTarget) string {
	return fmt.Sprintf("add this to %s manually:\n\n  %q: %s\n", t.path, t.key, t.value)
}

func runInstall(args []string) {
	fs := flag.NewFlagSet("install", flag.ExitOnError)
	targetName := fs.String("target", "claude", "which tool to wire up: claude or agy")
	settingsPath := fs.String("settings-path", "", "explicit settings file path")
	force := fs.Bool("force", false, "overwrite an existing statusline entry without prompting")
	dryRun := fs.Bool("dry-run", false, "print what would change without writing")
	yes := fs.Bool("yes", false, "answer yes to all prompts")
	_ = fs.Parse(args)

	t, err := resolveTarget(*targetName, *settingsPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "✗ %v\n", err)
		os.Exit(1)
	}

	raw, readErr := os.ReadFile(t.path)
	created := false
	if readErr != nil {
		if !os.IsNotExist(readErr) {
			fmt.Fprintf(os.Stderr, "✗ cannot read %s: %v\n", t.path, readErr)
			os.Exit(1)
		}
		if _, err := os.Stat(filepath.Dir(t.path)); err != nil {
			if !confirm(fmt.Sprintf("%s does not exist — is %s installed? Create it anyway?", filepath.Dir(t.path), t.name), *yes || *force) {
				os.Exit(1)
			}
			if !*dryRun {
				if err := os.MkdirAll(filepath.Dir(t.path), 0o755); err != nil {
					fmt.Fprintf(os.Stderr, "✗ %v\n", err)
					os.Exit(1)
				}
			}
		}
		raw = []byte("{}\n")
		created = true
	}

	// Parse gate: never edit a file we can't fully parse (JSONC, trailing
	// commas, etc.) — print the snippet instead.
	var gate map[string]json.RawMessage
	if err := json.Unmarshal(raw, &gate); err != nil {
		fmt.Fprintf(os.Stderr, "✗ %s is not plain JSON (%v)\n\n%s", t.path, err, manualSnippet(t))
		os.Exit(1)
	}

	_, valStart, valEnd, found, err := findTopLevelKeySpan(raw, t.key)
	if err != nil {
		fmt.Fprintf(os.Stderr, "✗ %v\n\n%s", err, manualSnippet(t))
		os.Exit(1)
	}

	var updated []byte
	switch {
	case found && compactJSON(raw[valStart:valEnd]) == compactJSON([]byte(t.value)):
		fmt.Printf("✓ Already installed in %s — nothing to do.\n", t.path)
		verifyInstall(t)
		return
	case found:
		fmt.Printf("Existing %s entry in %s:\n  %s\n", t.key, t.path, string(raw[valStart:valEnd]))
		if !*force && !confirm("Overwrite it?", *yes) {
			os.Exit(1)
		}
		updated = replaceKeyValue(raw, valStart, valEnd, t.value)
	default:
		updated, err = insertTopLevelKey(raw, t.key, t.value)
		if err != nil {
			fmt.Fprintf(os.Stderr, "✗ %v\n\n%s", err, manualSnippet(t))
			os.Exit(1)
		}
	}

	// The result must itself parse — belt and braces before touching disk.
	var check map[string]json.RawMessage
	if err := json.Unmarshal(updated, &check); err != nil {
		fmt.Fprintf(os.Stderr, "✗ internal error: spliced JSON does not parse (%v)\n\n%s", err, manualSnippet(t))
		os.Exit(1)
	}

	if *dryRun {
		fmt.Printf("dry run — would write %s with:\n  %q: %s\n", t.path, t.key, t.value)
		return
	}

	if !created {
		if bak, err := backupFile(t.path); err == nil {
			fmt.Printf("✓ Backed up %s → %s\n", t.path, filepath.Base(bak))
		}
	}
	if err := writeFileAtomic(t.path, updated); err != nil {
		fmt.Fprintf(os.Stderr, "✗ cannot write %s: %v\n", t.path, err)
		os.Exit(1)
	}
	fmt.Printf("✓ Added %s to %s\n", t.key, t.path)
	verifyInstall(t)
}

func compactJSON(b []byte) string {
	var buf bytes.Buffer
	if err := json.Compact(&buf, b); err != nil {
		return string(b)
	}
	return buf.String()
}

// verifyInstall pipes a sample payload through the *configured command
// string* via the shell — testing PATH resolution exactly as the host tool
// will — and shows the rendered result.
func verifyInstall(t installTarget) {
	var cmdStr string
	if t.name == "claude" {
		var v struct {
			Command string `json:"command"`
		}
		_ = json.Unmarshal([]byte(t.value), &v)
		cmdStr = v.Command
	} else {
		_ = json.Unmarshal([]byte(t.value), &cmdStr)
	}
	payload, err := json.Marshal(samplePayload())
	if err != nil {
		return
	}
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("cmd", "/c", cmdStr)
	} else {
		cmd = exec.Command("/bin/sh", "-c", cmdStr)
	}
	cmd.Stdin = bytes.NewReader(payload)
	out, err := cmd.Output()
	if err != nil {
		fmt.Printf("✗ Verification failed running %q: %v\n", cmdStr, err)
		return
	}
	fmt.Println("✓ Verified — sample render:")
	fmt.Println()
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		fmt.Println("  " + line)
	}
	fmt.Println()
	fmt.Println("Restart Claude Code to pick it up.")
	fmt.Println("Customize anytime:  claude-statusline configure")
	fmt.Println("Update checks:      notify (configure via [update] in config.toml)")
}

func runUninstall(args []string) {
	fs := flag.NewFlagSet("uninstall", flag.ExitOnError)
	targetName := fs.String("target", "claude", "which tool to unwire: claude or agy")
	settingsPath := fs.String("settings-path", "", "explicit settings file path")
	restore := fs.Bool("restore", false, "restore settings.json.bak instead of editing")
	yes := fs.Bool("yes", false, "answer yes to all prompts")
	_ = fs.Parse(args)

	t, err := resolveTarget(*targetName, *settingsPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "✗ %v\n", err)
		os.Exit(1)
	}

	if *restore {
		bak := t.path + ".bak"
		if _, err := os.Stat(bak); err != nil {
			fmt.Fprintf(os.Stderr, "✗ no backup at %s\n", bak)
			os.Exit(1)
		}
		if !confirm(fmt.Sprintf("Replace %s with %s? (the backup may be stale)", t.path, bak), *yes) {
			os.Exit(1)
		}
		data, err := os.ReadFile(bak)
		if err == nil {
			err = writeFileAtomic(t.path, data)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "✗ restore failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("✓ Restored %s from backup\n", t.path)
		return
	}

	raw, err := os.ReadFile(t.path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "✗ cannot read %s: %v\n", t.path, err)
		os.Exit(1)
	}
	var gate map[string]json.RawMessage
	if err := json.Unmarshal(raw, &gate); err != nil {
		fmt.Fprintf(os.Stderr, "✗ %s is not plain JSON (%v) — remove the %q key manually\n", t.path, err, t.key)
		os.Exit(1)
	}
	updated, found, err := deleteTopLevelKey(raw, t.key)
	if err != nil {
		fmt.Fprintf(os.Stderr, "✗ %v\n", err)
		os.Exit(1)
	}
	if !found {
		fmt.Printf("Nothing to uninstall — no %q key in %s.\n", t.key, t.path)
		return
	}
	var check map[string]json.RawMessage
	if err := json.Unmarshal(updated, &check); err != nil {
		fmt.Fprintf(os.Stderr, "✗ internal error: edited JSON does not parse (%v) — remove the %q key manually\n", err, t.key)
		os.Exit(1)
	}
	if bak, err := backupFile(t.path); err == nil {
		fmt.Printf("✓ Backed up %s → %s\n", t.path, filepath.Base(bak))
	}
	if err := writeFileAtomic(t.path, updated); err != nil {
		fmt.Fprintf(os.Stderr, "✗ cannot write %s: %v\n", t.path, err)
		os.Exit(1)
	}
	fmt.Printf("✓ Removed %s from %s\n", t.key, t.path)
}
