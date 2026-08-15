//go:build windows

package autostart

import (
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
)

type runnerCall struct {
	name string
	args []string
}

type fakeRunner struct {
	calls []runnerCall
	run   func(name string, args ...string) ([]byte, error)
}

func (r *fakeRunner) Run(name string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, runnerCall{name: name, args: append([]string(nil), args...)})
	return r.run(name, args...)
}

func taskXML(command, arguments string) []byte {
	return []byte(`<?xml version="1.0" encoding="UTF-16"?>
<Task xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
<Triggers><LogonTrigger><Enabled>true</Enabled></LogonTrigger></Triggers><Settings><Enabled>true</Enabled></Settings>
<Actions Context="Author"><Exec><Command>` + command + `</Command><Arguments>` + arguments + `</Arguments></Exec></Actions>
</Task>`)
}

func TestWindowsEnableQuotesSpacePathAndVerifiesTask(t *testing.T) {
	const executable = `C:\Program Files\ai gateway\ai-gateway.exe`
	runner := &fakeRunner{}
	runner.run = func(_ string, args ...string) ([]byte, error) {
		if len(args) > 0 && strings.EqualFold(args[0], "/Query") {
			return taskXML(executable, ServeArgument), nil
		}
		return []byte("SUCCESS"), nil
	}
	r := &windowsRegistrar{executable: executable, runner: runner}
	if err := r.Enable(); err != nil {
		t.Fatal(err)
	}
	if len(runner.calls) != 2 {
		t.Fatalf("calls = %d, want create + query", len(runner.calls))
	}
	wantTaskRun := `"C:\Program Files\ai gateway\ai-gateway.exe" serve`
	create := runner.calls[0]
	index := -1
	for i, arg := range create.args {
		if strings.EqualFold(arg, "/TR") {
			index = i + 1
			break
		}
	}
	if index <= 0 || index >= len(create.args) || create.args[index] != wantTaskRun {
		t.Fatalf("create args = %#v, missing exact /TR %q", create.args, wantTaskRun)
	}
	for _, required := range []string{"/SC", "ONLOGON", "/IT", "/RL", "LIMITED"} {
		found := false
		for _, arg := range create.args {
			if strings.EqualFold(arg, required) {
				found = true
			}
		}
		if !found {
			t.Errorf("create args missing %q: %#v", required, create.args)
		}
	}
}

func TestParseWindowsTaskRejectsWrongPathArgumentAndTrigger(t *testing.T) {
	good, err := parseWindowsTask(taskXML(`C:\Apps\ai-gateway.exe`, "serve"))
	if err != nil {
		t.Fatal(err)
	}
	if good.Executable != `C:\Apps\ai-gateway.exe` || !reflect.DeepEqual(good.Arguments, []string{"serve"}) || good.Issue != "" {
		t.Fatalf("good registration = %+v", good)
	}

	missingTrigger := []byte(`<Task><Triggers></Triggers><Settings><Enabled>true</Enabled></Settings><Actions><Exec><Command>C:\Apps\ai-gateway.exe</Command><Arguments>serve</Arguments></Exec></Actions></Task>`)
	bad, err := parseWindowsTask(missingTrigger)
	if err != nil {
		t.Fatal(err)
	}
	if bad.Issue == "" {
		t.Fatal("missing logon trigger was accepted")
	}
}

func TestWindowsEnableFailsWhenReadbackDoesNotMatch(t *testing.T) {
	runner := &fakeRunner{run: func(_ string, args ...string) ([]byte, error) {
		if len(args) > 0 && strings.EqualFold(args[0], "/Query") {
			return taskXML(`C:\Old\ai-gateway.exe`, "serve"), nil
		}
		return nil, nil
	}}
	r := &windowsRegistrar{executable: `C:\New\ai-gateway.exe`, runner: runner}
	if err := r.Enable(); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("Enable error = %v, want readback mismatch", err)
	}
}

func TestTaskNotFoundOnlyAcceptsFileNotFoundHRESULT(t *testing.T) {
	if taskNotFound(errors.New("plain error")) {
		t.Fatal("plain error treated as task-not-found")
	}
}

func TestWindowsRealTaskEnableReadDisable(t *testing.T) {
	if os.Getenv("AI_GATEWAY_AUTOSTART_INTEGRATION") != "1" {
		t.Skip("set AI_GATEWAY_AUTOSTART_INTEGRATION=1 to mutate the current-user test task")
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(executable, " ") {
		t.Fatalf("integration executable path %q must contain a space", executable)
	}
	registrar := New(executable)
	before, err := registrar.Status()
	if err != nil {
		t.Fatal(err)
	}
	if before.Exists {
		t.Skip("the user already has an ai-gateway task; refusing to overwrite it")
	}
	t.Cleanup(func() {
		if err := registrar.Disable(); err != nil {
			t.Errorf("cleanup Disable: %v", err)
		}
	})
	if err := registrar.Enable(); err != nil {
		t.Fatal(err)
	}
	status, err := registrar.Status()
	if err != nil {
		t.Fatal(err)
	}
	if !status.Enabled || !status.Valid || !sameWindowsPath(status.Executable, executable) {
		t.Fatalf("registered status = %+v, executable = %q", status, executable)
	}
	if err := registrar.Disable(); err != nil {
		t.Fatal(err)
	}
	status, err = registrar.Status()
	if err != nil {
		t.Fatal(err)
	}
	if status.Exists {
		t.Fatalf("task remained after Disable: %+v", status)
	}
}
