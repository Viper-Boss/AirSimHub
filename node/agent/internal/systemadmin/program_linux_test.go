//go:build linux

package systemadmin

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// programFixture is the account fixture plus program support: a faked useradd
// that really appends to the fake-root passwd (so the uid/gid read-back runs),
// and a working fake binary tree the installer can inspect.
func programFixture(t *testing.T) (*Manager, *[]string) {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("root-owned fixture requires root")
	}
	manager, calls := fixture(t)
	manager.Run = func(ctx context.Context, input, name string, args ...string) (string, error) {
		*calls = append(*calls, name+" "+strings.Join(args, " "))
		switch {
		case name == "/usr/sbin/useradd":
			passwd := filepath.Join(manager.Root, "etc/passwd")
			previous, err := os.ReadFile(passwd)
			if err != nil {
				return "", err
			}
			user := args[len(args)-1]
			line := user + ":x:150:150::/var/lib/nassimhub-programs:" + "/usr/sbin/nologin\n"
			return "", os.WriteFile(passwd, append(previous, []byte(line)...), 0600)
		case strings.Contains(name, "systemctl") && len(args) > 0 && args[0] == "is-active":
			return "active\n", nil
		}
		return "", nil
	}
	binary := filepath.Join(manager.Root, programBinRoot, "weather")
	if err := os.MkdirAll(binary, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binary, "run"), []byte("#!/bin/sh\nsleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return manager, calls
}

func installRequest() Request {
	return Request{Action: "program_install", Program: &ProgramRequest{
		Name:      "weather",
		ExecStart: []string{"/usr/local/lib/nassimhub-programs/weather/run"},
	}}
}

func TestProgramInstallCreatesUserUnitAndData(t *testing.T) {
	manager, calls := programFixture(t)
	status, err := manager.Execute(context.Background(), installRequest())
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	unit := filepath.Join(manager.Root, "etc/systemd/system/nassimhub-program-weather.service")
	text, err := os.ReadFile(unit)
	if err != nil || !strings.Contains(string(text), "User=nassimhub-p-weather") {
		t.Fatalf("unit missing or wrong: %v", err)
	}
	info, err := os.Stat(filepath.Join(manager.Root, "var/lib/nassimhub-programs/weather"))
	if err != nil || info.Mode().Perm() != 0o750 {
		t.Fatalf("data directory missing or wrong mode: %v %v", err, info)
	}
	joined := strings.Join(*calls, "\n")
	for _, want := range []string{
		"/usr/sbin/useradd --system --user-group --home-dir /var/lib/nassimhub-programs/weather --shell /usr/sbin/nologin nassimhub-p-weather",
		"/usr/bin/systemctl daemon-reload",
		"/usr/bin/systemctl enable --now nassimhub-program-weather.service",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing command %q in:\n%s", want, joined)
		}
	}
	if len(status.Programs) != 1 || status.Programs[0].Name != "weather" || status.Programs[0].MemoryMaxMiB != 64 {
		t.Fatalf("status after install: %#v", status.Programs)
	}
}

func TestProgramInstallRefusesUnplacedOrWritableBinaries(t *testing.T) {
	for name, prepare := range map[string]func(t *testing.T, m *Manager){
		"outside the program tree": func(t *testing.T, m *Manager) {
			os.MkdirAll(filepath.Join(m.Root, "opt"), 0o755)
			os.WriteFile(filepath.Join(m.Root, "opt/run"), []byte("#!/bin/sh\n"), 0o755)
		},
		"group-writable binary": func(t *testing.T, m *Manager) {
			path := filepath.Join(m.Root, programBinRoot, "weather", "run")
			os.Chmod(path, 0o775)
		},
		"not executable": func(t *testing.T, m *Manager) {
			path := filepath.Join(m.Root, programBinRoot, "weather", "run")
			os.Chmod(path, 0o644)
		},
		"missing binary": func(t *testing.T, m *Manager) {
			os.Remove(filepath.Join(m.Root, programBinRoot, "weather", "run"))
		},
	} {
		manager, _ := programFixture(t)
		request := installRequest()
		if name == "outside the program tree" {
			request.Program.ExecStart = []string{"/opt/run"}
		}
		prepare(t, manager)
		if _, err := manager.Execute(context.Background(), request); err == nil {
			t.Fatalf("%s: install accepted", name)
		}
		if _, err := os.Stat(filepath.Join(manager.Root, "etc/systemd/system/nassimhub-program-weather.service")); err == nil {
			t.Fatalf("%s: a unit was written anyway", name)
		}
	}
}

func TestProgramInstallFailureCleansTheUnitUp(t *testing.T) {
	manager, _ := programFixture(t)
	// enable --now fails: nothing of the program may remain registered.
	manager.Run = func(ctx context.Context, input, name string, args ...string) (string, error) {
		if manager.Run != nil {
			// keep the useradd behaviour of the fixture
			if name == "/usr/sbin/useradd" {
				passwd := filepath.Join(manager.Root, "etc/passwd")
				previous, err := os.ReadFile(passwd)
				if err != nil {
					return "", err
				}
				return "", os.WriteFile(passwd, append(previous, []byte("nassimhub-p-weather:x:150:150::/var/lib/nassimhub-programs:/usr/sbin/nologin\n")...), 0600)
			}
			if strings.Contains(name, "systemctl") && len(args) > 0 && (args[0] == "enable" || args[0] == "daemon-reload") {
				if args[0] == "enable" {
					return "", os.ErrPermission
				}
				return "", nil
			}
			if strings.Contains(name, "systemctl") && args[0] == "is-active" {
				return "active\n", nil
			}
		}
		return "", nil
	}
	if _, err := manager.Execute(context.Background(), installRequest()); err == nil {
		t.Fatal("install succeeded although enable failed")
	}
	if _, err := os.Stat(filepath.Join(manager.Root, "etc/systemd/system/nassimhub-program-weather.service")); err == nil {
		t.Fatal("a failed install left its unit behind")
	}
}

func TestProgramRemoveStopsDeletesAndUnwires(t *testing.T) {
	manager, calls := programFixture(t)
	if _, err := manager.Execute(context.Background(), installRequest()); err != nil {
		t.Fatalf("install: %v", err)
	}
	status, err := manager.Execute(context.Background(), Request{Action: "program_remove", Program: &ProgramRequest{Name: "weather"}})
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if len(status.Programs) != 0 {
		t.Fatalf("program still listed: %#v", status.Programs)
	}
	if _, err := os.Stat(filepath.Join(manager.Root, "etc/systemd/system/nassimhub-program-weather.service")); err == nil {
		t.Fatal("unit still present after remove")
	}
	if _, err := os.Stat(filepath.Join(manager.Root, "var/lib/nassimhub-programs/weather")); err == nil {
		t.Fatal("data directory still present after remove")
	}
	joined := strings.Join(*calls, "\n")
	for _, want := range []string{
		"/usr/bin/systemctl disable --now nassimhub-program-weather.service",
		"/usr/sbin/userdel nassimhub-p-weather",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing command %q in:\n%s", want, joined)
		}
	}
}

func TestProgramRemoveOfNothingIsHonest(t *testing.T) {
	manager, _ := programFixture(t)
	if _, err := manager.Execute(context.Background(), Request{Action: "program_remove", Program: &ProgramRequest{Name: "ghost"}}); err != nil {
		t.Fatalf("removing an uninstalled program must not fail: %v", err)
	}
}
