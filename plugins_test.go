package main

import (
	"os"
	"path/filepath"
	"testing"
)

// writeScript drops an executable shell script into a temp dir.
func writeScript(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "plugin.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPluginSingleField(t *testing.T) {
	def := pluginDef{ID: "hello", Command: writeScript(t, `echo "hello world"`)}
	if got := runPluginRaw(def, payload{}); got != "hello world" {
		t.Errorf("runPluginRaw = %q, want %q", got, "hello world")
	}
}

func TestPluginMultiField(t *testing.T) {
	clearPluginCache()
	def := pluginDef{
		Command: writeScript(t, "echo cpu:42%\necho mem: 73%"),
		Fields:  []pluginField{{ID: "cpu"}, {ID: "mem"}},
	}
	if got := runPluginField(def, payload{}, "cpu"); got != "42%" {
		t.Errorf("cpu = %q, want %q", got, "42%")
	}
	if got := runPluginField(def, payload{}, "mem"); got != "73%" {
		t.Errorf("mem = %q, want %q", got, "73%")
	}
}

func TestPluginTimeout(t *testing.T) {
	def := pluginDef{ID: "slow", Command: writeScript(t, "sleep 5; echo done"), TimeoutMS: 50}
	if got := runPluginRaw(def, payload{}); got != "" {
		t.Errorf("timed-out plugin should return empty, got %q", got)
	}
}

func TestPluginNonZeroExit(t *testing.T) {
	def := pluginDef{ID: "fail", Command: writeScript(t, "echo oops; exit 3")}
	if got := runPluginRaw(def, payload{}); got != "" {
		t.Errorf("failing plugin should return empty, got %q", got)
	}
}

func TestPluginMissingExecutable(t *testing.T) {
	def := pluginDef{ID: "ghost", Command: "/nonexistent/plugin.sh"}
	if got := runPluginRaw(def, payload{}); got != "" {
		t.Errorf("missing plugin should return empty, got %q", got)
	}
}

func TestParseKeyValueOutput(t *testing.T) {
	out := parseKeyValueOutput("cpu:42\n  mem : 73 \nnocolon\n:novalue\nurl:http://x:1\n")
	if out["cpu"] != "42" {
		t.Errorf("cpu = %q", out["cpu"])
	}
	if out["mem"] != "73" {
		t.Errorf("whitespace not trimmed: %q", out["mem"])
	}
	if _, ok := out["nocolon"]; ok {
		t.Error("line without colon should be skipped")
	}
	if _, ok := out[""]; ok {
		t.Error("empty key should be skipped")
	}
	if out["url"] != "http://x:1" {
		t.Errorf("only first colon splits: %q", out["url"])
	}
}
