package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// captureStdout replaces os.Stdout with a pipe for the duration of fn and
// returns everything fn wrote.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	defer func() { os.Stdout = old }()

	done := make(chan string, 1)
	go func() {
		var b strings.Builder
		buf := make([]byte, 4096)
		for {
			n, err := r.Read(buf)
			if n > 0 {
				b.Write(buf[:n])
			}
			if err != nil {
				break
			}
		}
		done <- b.String()
	}()

	fn()
	_ = w.Close()
	return <-done
}

// TestMatrixSubcommandOutput verifies that `runMatrix` prints the curated
// scenario panes and flags the narrow scenario as overflowing.
func TestMatrixSubcommandOutput(t *testing.T) {
	configDirOverride = t.TempDir()

	out := captureStdout(t, func() {
		runMatrix([]string{})
	})

	scs := curatedScenarios(scenarioNow())
	for _, sc := range scs {
		if !strings.Contains(out, "── "+sc.Name+" ──") {
			t.Errorf("matrix output missing header for %q", sc.Name)
		}
	}

	// The tiny full-cascade scenario at 40 cols overflows; the matrix should
	// flag it so narrow layouts are visible at a glance.
	foundOverflow := false
	lines := strings.Split(out, "\n")
	for i, line := range lines {
		if strings.Contains(line, "tiny · full · cascade") {
			for j := i; j < len(lines) && j < i+5; j++ {
				if strings.Contains(lines[j], "OVERFLOWS") {
					foundOverflow = true
					break
				}
			}
			break
		}
	}
	if !foundOverflow {
		t.Errorf("expected tiny · full · cascade to be flagged as OVERFLOWS; output:\n%s", out)
	}
}

// TestMatrixReflowValidation rejects invalid --reflow values at the CLI level.
func TestMatrixReflowValidation(t *testing.T) {
	if os.Getenv("BE_MATRIX_INVALID_REFLOW") == "1" {
		configDirOverride = t.TempDir()
		runMatrix([]string{"--reflow", "nope"})
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestMatrixReflowValidation")
	cmd.Env = append(os.Environ(), "BE_MATRIX_INVALID_REFLOW=1")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected non-zero exit for invalid --reflow, got none; output:\n%s", out)
	}
	if !strings.Contains(string(out), "invalid --reflow") {
		t.Errorf("expected invalid --reflow error, got:\n%s", out)
	}
}
