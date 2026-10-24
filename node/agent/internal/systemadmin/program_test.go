package systemadmin

import (
	"strings"
	"testing"
)

func TestValidateProgramInstallRequest(t *testing.T) {
	good := func() *ProgramRequest {
		return &ProgramRequest{Name: "weather", ExecStart: []string{"/usr/local/lib/nassimhub-programs/weather/run", "--once"},
			Description: "fetches weather", MemoryMaxMiB: 96, CPUWeight: 30, TasksMax: 128}
	}
	if err := validateProgramRequest(good(), true); err != nil {
		t.Fatalf("valid request refused: %v", err)
	}
	defaults := &ProgramRequest{Name: "weather", ExecStart: []string{"/usr/local/lib/nassimhub-programs/weather/run"}}
	if err := validateProgramRequest(defaults, true); err != nil {
		t.Fatalf("defaults refused: %v", err)
	}
	if defaults.MemoryMaxMiB != 64 || defaults.CPUWeight != 20 || defaults.TasksMax != 64 {
		t.Fatalf("defaults not applied: %#v", defaults)
	}
	for name, mutate := range map[string]func(*ProgramRequest){
		"missing request":     func(r *ProgramRequest) { *r = ProgramRequest{} },
		"bad name":            func(r *ProgramRequest) { r.Name = "../etc" },
		"name too long":       func(r *ProgramRequest) { r.Name = "a" + strings.Repeat("b", 21) },
		"uppercase name":      func(r *ProgramRequest) { r.Name = "Weather" },
		"no exec":             func(r *ProgramRequest) { r.ExecStart = nil },
		"outside binary root": func(r *ProgramRequest) { r.ExecStart = []string{"/bin/sh"} },
		"sibling directory":   func(r *ProgramRequest) { r.ExecStart = []string{"/usr/local/lib/nassimhub-programs/other/run"} },
		"traversal":           func(r *ProgramRequest) { r.ExecStart = []string{"/usr/local/lib/nassimhub-programs/weather/../../x"} },
		"newline in arg":      func(r *ProgramRequest) { r.ExecStart = []string{"/usr/local/lib/nassimhub-programs/weather/run", "a\nb"} },
		"too many args":       func(r *ProgramRequest) { r.ExecStart = append(make([]string, 33), "x") },
		"memory over clamp":   func(r *ProgramRequest) { r.MemoryMaxMiB = 512 },
		"memory under clamp":  func(r *ProgramRequest) { r.MemoryMaxMiB = 8 },
		"cpu weight over":     func(r *ProgramRequest) { r.CPUWeight = 101 },
		"tasks over":          func(r *ProgramRequest) { r.TasksMax = 999 },
		"two-line description": func(r *ProgramRequest) { r.Description = "one\ntwo" },
	} {
		request := good()
		mutate(request)
		if err := validateProgramRequest(request, true); err == nil {
			t.Fatalf("%s: accepted an invalid request", name)
		}
	}
}

func TestValidateProgramRemoveRequest(t *testing.T) {
	if err := validateProgramRequest(&ProgramRequest{Name: "weather"}, false); err != nil {
		t.Fatalf("bare remove refused: %v", err)
	}
	if err := validateProgramRequest(&ProgramRequest{Name: "weather", ExecStart: []string{"/usr/local/lib/nassimhub-programs/weather/run"}}, false); err == nil {
		t.Fatal("remove accepted an exec_start")
	}
	if err := validateProgramRequest(&ProgramRequest{Name: "../x"}, false); err == nil {
		t.Fatal("remove accepted a bad name")
	}
}

func TestProgramUnitIsFullySandboxed(t *testing.T) {
	text := programUnitText(ProgramRequest{Name: "weather", ExecStart: []string{"/usr/local/lib/nassimhub-programs/weather/run", "--city \"Berlin\""},
		Description: "weather", MemoryMaxMiB: 96, CPUWeight: 30, TasksMax: 128})
	for _, want := range []string{
		"User=nassimhub-p-weather\n", "Group=nassimhub-p-weather\n",
		`ExecStart="/usr/local/lib/nassimhub-programs/weather/run" "--city \"Berlin\""` + "\n",
		"MemoryMax=96M\n", "MemorySwapMax=0\n", "CPUWeight=30\n", "TasksMax=128\n",
		"NoNewPrivileges=yes\n", "ProtectSystem=strict\n", "PrivateTmp=yes\n", "PrivateDevices=yes\n",
		"ProtectKernelModules=yes\n", "RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6\n",
		"CapabilityBoundingSet=\n", "SystemCallFilter=@system-service\n",
		"ReadWritePaths=/var/lib/nassimhub-programs/weather\n",
		"StartLimitBurst=5\n", "SyslogIdentifier=nassimhub-program-weather\n",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("unit is missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "\nExecStartPre") || strings.Contains(text, "\nExecStopPost") {
		t.Fatal("request must not be able to smuggle extra unit hooks")
	}
}
