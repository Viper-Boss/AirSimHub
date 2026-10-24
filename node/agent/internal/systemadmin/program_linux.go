//go:build linux

package systemadmin

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// programInstall validates, sandboxes and starts one third-party program:
// locked system user, data directory, generated unit, daemon-reload, enable.
// Nothing from the request reaches the unit unfiltered; validateProgramRequest
// has already clamped the resource envelope.
func (m *Manager) programInstall(ctx context.Context, r ProgramRequest) (Status, error) {
	if err := validateProgramRequest(&r, true); err != nil {
		return Status{}, err
	}
	binary := filepath.Clean("/" + r.ExecStart[0])
	info, err := os.Lstat(m.path(binary))
	if err != nil || !info.Mode().IsRegular() || info.Mode()&0o111 == 0 || info.Mode().Perm()&0o022 != 0 {
		return Status{}, errors.New("program executable not found, not executable, or writable by group/others")
	}
	user := programUser(r.Name)
	uid, gid, err := m.ensureProgramUser(ctx, user, programDataRoot+"/"+r.Name)
	if err != nil {
		return Status{}, err
	}
	data := m.path(programDataRoot + "/" + r.Name)
	if err := os.MkdirAll(data, 0o750); err != nil {
		return Status{}, err
	}
	if err := os.Chown(data, uid, gid); err != nil {
		return Status{}, err
	}
	unit := programUnit(r.Name)
	if err := os.MkdirAll(m.path("/etc/systemd/system"), 0o755); err != nil {
		return Status{}, err
	}
	if err := m.writeRegularAtomic(m.path("/etc/systemd/system/"+unit), []byte(programUnitText(r)), 0o644); err != nil {
		return Status{}, err
	}
	if _, err := m.exec(ctx, "", "/usr/bin/systemctl", "daemon-reload"); err != nil {
		return Status{}, err
	}
	if _, err := m.exec(ctx, "", "/usr/bin/systemctl", "enable", "--now", unit); err != nil {
		// Do not leave a half-installed unit behind: an enabled-but-failing
		// program must not survive a refused install.
		os.Remove(m.path("/etc/systemd/system/" + unit))
		if _, discardErr := m.exec(ctx, "", "/usr/bin/systemctl", "daemon-reload"); discardErr != nil {
			return Status{}, errors.New("the program unit could not be started and cleanup failed; run systemctl daemon-reload")
		}
		return Status{}, errors.New("the program unit could not be started; the executable must run in the sandbox as-is")
	}
	return m.status(ctx)
}

// programRemove stops the unit, deletes it and the program's data directory,
// and removes the locked user. Removing a program erases its data by design.
func (m *Manager) programRemove(ctx context.Context, r ProgramRequest) (Status, error) {
	if err := validateProgramRequest(&r, false); err != nil {
		return Status{}, err
	}
	unit := programUnit(r.Name)
	unitPath := m.path("/etc/systemd/system/" + unit)
	if _, err := os.Lstat(unitPath); err == nil {
		if _, err := m.exec(ctx, "", "/usr/bin/systemctl", "disable", "--now", unit); err != nil {
			return Status{}, errors.New("the program could not be stopped; remove it after stopping it manually")
		}
		if err := os.Remove(unitPath); err != nil {
			return Status{}, err
		}
		if _, err := m.exec(ctx, "", "/usr/bin/systemctl", "daemon-reload"); err != nil {
			return Status{}, err
		}
	}
	data := filepath.Clean(m.path(programDataRoot + "/" + r.Name))
	if strings.HasPrefix(data, filepath.Clean(m.path(programDataRoot))+"/") && data != filepath.Clean(m.path(programDataRoot)) {
		if err := os.RemoveAll(data); err != nil {
			return Status{}, err
		}
	}
	if m.programUserExists(programUser(r.Name)) {
		if _, err := m.exec(ctx, "", "/usr/sbin/userdel", programUser(r.Name)); err != nil {
			return Status{}, errors.New("program removed; its user is still logged in, delete it after its sessions end")
		}
	}
	return m.status(ctx)
}

// ensureProgramUser creates the locked system user once and returns its uid
// and gid, read back from /etc/passwd so a faked useradd in tests and a real
// one on the device behave identically.
func (m *Manager) ensureProgramUser(ctx context.Context, user, home string) (int, int, error) {
	if uid, gid, ok := m.lookupUser(user); ok {
		return uid, gid, nil
	}
	if _, err := m.exec(ctx, "", "/usr/sbin/useradd", "--system", "--user-group",
		"--home-dir", home, "--shell", "/usr/sbin/nologin", user); err != nil {
		return 0, 0, err
	}
	uid, gid, ok := m.lookupUser(user)
	if !ok {
		return 0, 0, errors.New("the program user was not created")
	}
	return uid, gid, nil
}

func (m *Manager) programUserExists(user string) bool {
	_, _, ok := m.lookupUser(user)
	return ok
}

// lookupUser reads the fake-root /etc/passwd the same way the account code
// does; it never consults the host's live user database.
func (m *Manager) lookupUser(name string) (int, int, bool) {
	data, err := readRegular(m.path("/etc/passwd"))
	if err != nil {
		return 0, 0, false
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Split(line, ":")
		if len(fields) != 7 || fields[0] != name {
			continue
		}
		uid, uidErr := strconv.Atoi(fields[2])
		gid, gidErr := strconv.Atoi(fields[3])
		if uidErr != nil || gidErr != nil {
			return 0, 0, false
		}
		return uid, gid, true
	}
	return 0, 0, false
}

// writeRegularAtomic installs file contents the way the SSH config code does:
// a fresh file, renamed into place, never following a symlink.
func (m *Manager) writeRegularAtomic(path string, content []byte, mode os.FileMode) error {
	temporary := path + ".nassimhub-new"
	if err := os.WriteFile(temporary, content, mode); err != nil {
		return err
	}
	if err := os.Chmod(temporary, mode); err != nil {
		os.Remove(temporary)
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		os.Remove(temporary)
		return err
	}
	return nil
}

// programStatusList reports every installed program unit with its live state.
func (m *Manager) programStatusList(ctx context.Context) []ProgramStatus {
	directory := m.path("/etc/systemd/system")
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil
	}
	var programs []ProgramStatus
	for _, entry := range entries {
		name := strings.TrimSuffix(entry.Name(), ".service")
		if !strings.HasPrefix(name, programUnitPrefix) || entry.IsDir() {
			continue
		}
		program := ProgramStatus{Name: strings.TrimPrefix(name, programUnitPrefix)}
		unit, err := os.ReadFile(filepath.Join(directory, entry.Name()))
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(unit), "\n") {
			switch {
			case strings.HasPrefix(line, "MemoryMax="):
				fmtSscanMiB(strings.TrimPrefix(line, "MemoryMax="), &program.MemoryMaxMiB)
			case strings.HasPrefix(line, "CPUWeight="):
				fmtSscanMiB(strings.TrimPrefix(line, "CPUWeight="), &program.CPUWeight)
			case strings.HasPrefix(line, "TasksMax="):
				fmtSscanMiB(strings.TrimPrefix(line, "TasksMax="), &program.TasksMax)
			}
		}
		if state, err := m.exec(ctx, "", "/usr/bin/systemctl", "is-active", entry.Name()); err == nil {
			program.State = strings.TrimSpace(state)
		} else {
			program.State = "inactive"
		}
		programs = append(programs, program)
	}
	sort.Slice(programs, func(i, j int) bool { return programs[i].Name < programs[j].Name })
	return programs
}

func fmtSscanMiB(text string, into *int) {
	value, err := strconv.Atoi(strings.TrimSuffix(strings.TrimSpace(text), "M"))
	if err == nil {
		*into = value
	}
}
