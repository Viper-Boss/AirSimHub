package msm8916

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// mmcliShim installs a fake mmcli as the next command on PATH, so the real
// query path (binary lookup, Output, stderr capture) runs end to end.
func mmcliShim(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "mmcli"), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatalf("write shim: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func captureLog(t *testing.T) (logFunc, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var records []string
	return func(format string, arguments ...any) {
		mu.Lock()
		defer mu.Unlock()
		records = append(records, fmt.Sprintf(format, arguments...))
	}, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), records...)
	}
}

// A failing mmcli must be logged with its arguments, its exit status and its
// own stderr; the returned error stays generic.
func TestMMCLIFailureIsLoggedWithItsStderr(t *testing.T) {
	mmcliShim(t, "echo 'error: modem not found' >&2\nexit 1\n")
	logf, records := captureLog(t)
	output, err := queryMMCLI(context.Background(), logf, "-K", "-L")
	if err == nil || output != "" || err.Error() != "ModemManager query failed" {
		t.Fatalf("output=%q err=%v", output, err)
	}
	logged := records()
	if len(logged) != 1 {
		t.Fatalf("expected one log record, got %q", logged)
	}
	for _, want := range []string{"mmcli -K -L failed", "exit status 1", "error: modem not found"} {
		if !strings.Contains(logged[0], want) {
			t.Fatalf("log record %q is missing %q", logged[0], want)
		}
	}
}

// An oversized output is discarded, and that too is logged rather than silent.
func TestMMCLIOversizedOutputIsLogged(t *testing.T) {
	mmcliShim(t, "head -c 70000 /dev/zero\n")
	logf, records := captureLog(t)
	output, err := queryMMCLI(context.Background(), logf, "-K", "-L")
	if err == nil || output != "" {
		t.Fatalf("oversized output was accepted: %q %v", output, err)
	}
	logged := records()
	if len(logged) != 1 || !strings.Contains(logged[0], "exceeded 64 KiB") {
		t.Fatalf("oversized output was not logged: %q", logged)
	}
}

// A missing mmcli binary is a start failure, still logged with context.
func TestMMCLIStartFailureIsLogged(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	logf, records := captureLog(t)
	if _, err := queryMMCLI(context.Background(), logf, "-K", "-L"); err == nil {
		t.Fatal("missing mmcli was not an error")
	}
	logged := records()
	if len(logged) != 1 || !strings.Contains(logged[0], "mmcli -K -L failed") {
		t.Fatalf("start failure was not logged: %q", logged)
	}
}

// stderrDetail trims hard and collapses to one line. The only honest way to
// get a real *exec.ExitError with stderr is to run a failing command.
func TestStderrDetailIsTrimmedAndCollapsed(t *testing.T) {
	long := strings.Repeat("y", 900)
	message := filepath.Join(t.TempDir(), "message")
	if err := os.WriteFile(message, []byte("  line one\nline two "+long), 0o644); err != nil {
		t.Fatalf("write message: %v", err)
	}
	_, err := exec.Command("/bin/sh", "-c", `cat "$1" >&2; exit 7`, "sh", message).Output()
	if err == nil {
		t.Fatal("command was expected to fail")
	}
	got := stderrDetail(err)
	if !strings.HasPrefix(got, " stderr: line one line two ") {
		t.Fatalf("detail was not collapsed onto one line: %q", got)
	}
	if len(got) > 600 {
		t.Fatalf("detail was not trimmed: %d bytes", len(got))
	}
}
