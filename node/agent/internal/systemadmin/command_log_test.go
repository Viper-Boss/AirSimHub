//go:build linux

package systemadmin

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// The log must carry the failing command line and the tool's own stderr, and
// must never carry the stdin input: chpasswd receives account passwords there.
func TestFailedCommandLogsItsStderrButNeverItsStdin(t *testing.T) {
	var mu sync.Mutex
	var logged []string
	manager := &Manager{Root: t.TempDir(), Logf: func(format string, arguments ...any) {
		mu.Lock()
		defer mu.Unlock()
		logged = append(logged, fmt.Sprintf(format, arguments...))
	}}
	_, err := manager.exec(context.Background(),
		"alice:sup3r-secret-password\n",
		"/bin/sh", "-c", "echo modem-manager-refused >&2; exit 4")
	if err == nil {
		t.Fatal("command was expected to fail")
	}
	if len(logged) != 1 {
		t.Fatalf("expected exactly one log record, got %d: %q", len(logged), logged)
	}
	record := logged[0]
	for _, want := range []string{"/bin/sh", "-c", "exit 4", "modem-manager-refused"} {
		if !strings.Contains(record, want) {
			t.Fatalf("log record %q is missing %q", record, want)
		}
	}
	if strings.Contains(record, "sup3r-secret-password") {
		t.Fatalf("stdin content leaked into the log: %q", record)
	}
}

// A command that cannot even start (no binary) is logged with the start error.
func TestUnstartableCommandIsLogged(t *testing.T) {
	var mu sync.Mutex
	var logged []string
	manager := &Manager{Root: t.TempDir(), Logf: func(format string, arguments ...any) {
		mu.Lock()
		defer mu.Unlock()
		logged = append(logged, fmt.Sprintf(format, arguments...))
	}}
	_, err := manager.exec(context.Background(), "", "/no/such/binary", "--flag")
	if err == nil {
		t.Fatal("command was expected to fail")
	}
	if len(logged) != 1 || !strings.Contains(logged[0], "/no/such/binary") {
		t.Fatalf("start failure was not logged: %q", logged)
	}
}

func TestStderrDetailIsTrimmedAndCollapsed(t *testing.T) {
	long := strings.Repeat("x", 900)
	got := stderrDetail([]byte("  line one\nline two  " + long + "\n"))
	if !strings.HasPrefix(got, " stderr: line one line two ") {
		t.Fatalf("detail was not collapsed onto one line: %q", got)
	}
	if len(got) > 600 {
		t.Fatalf("detail was not trimmed: %d bytes", len(got))
	}
	if stderrDetail(nil) != "" || stderrDetail([]byte("   \n ")) != "" {
		t.Fatal("empty stderr must produce no detail")
	}
}
