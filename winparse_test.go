package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestSplitWindowsCommandLine(t *testing.T) {
	cases := map[string][]string{
		`C:\Windows\System32\remotecmd-cli-2.exe  daemon start -name radioalto-prod-2 `: {`C:\Windows\System32\remotecmd-cli-2.exe`, "daemon", "start", "-name", "radioalto-prod-2"},
		`"C:\Program Files\rc\remotecmd-cli.exe" daemon start`:                          {`C:\Program Files\rc\remotecmd-cli.exe`, "daemon", "start"},
		`rc.exe relay daemon start -port 3032`:                                          {"rc.exe", "relay", "daemon", "start", "-port", "3032"},
	}
	for in, want := range cases {
		if got := splitWindowsCommandLine(in); !reflect.DeepEqual(got, want) {
			t.Errorf("split(%q) = %q, want %q", in, got, want)
		}
	}
}

// radioalto's real process list: two daemons on separate binaries.
const radioaltoCIM = `[
 {"ProcessId":2732,"ExecutablePath":"C:\\Windows\\System32\\remotecmd-cli-2.exe","CommandLine":"C:\\Windows\\System32\\remotecmd-cli-2.exe  daemon start -name radioalto-prod-2 "},
 {"ProcessId":3464,"ExecutablePath":"C:\\Windows\\System32\\remotecmd-cli.exe","CommandLine":"C:\\Windows\\System32\\remotecmd-cli.exe  daemon start"},
 {"ProcessId":9000,"ExecutablePath":"C:\\Windows\\System32\\cmd.exe","CommandLine":"cmd.exe /c start-rcmd-daemon.bat"},
 {"ProcessId":4242,"ExecutablePath":"C:\\Temp\\rc.exe","CommandLine":"C:\\Temp\\rc.exe daemon update --name radioalto-prod-2"}
]`

func TestParseWindowsProcs(t *testing.T) {
	all := parseWindowsProcs(radioaltoCIM, "daemon", "", 4242)
	if len(all) != 2 {
		t.Fatalf("all daemons: got %+v", all)
	}
	only := parseWindowsProcs(radioaltoCIM, "daemon", "radioalto-prod-2", 4242)
	if len(only) != 1 || only[0].PID != 2732 || only[0].Exe != `C:\Windows\System32\remotecmd-cli-2.exe` ||
		!reflect.DeepEqual(only[0].Args[1:], []string{"daemon", "start", "-name", "radioalto-prod-2"}) {
		t.Errorf("--name radioalto-prod-2: got %+v", only)
	}
	one := parseWindowsProcs(`{"ProcessId":3464,"ExecutablePath":"C:\\rc.exe","CommandLine":"C:\\rc.exe daemon start"}`, "daemon", "", 1)
	if len(one) != 1 || one[0].PID != 3464 {
		t.Errorf("single-object JSON: got %+v", one)
	}
}

// radioalto runs an unnamed daemon and a --name one; --pid picks just one.
func TestSelectPID(t *testing.T) {
	procs := parseWindowsProcs(radioaltoCIM, "daemon", "", 4242)
	got, err := selectPID(procs, 3464)
	if err != nil || len(got) != 1 || got[0].Exe != `C:\Windows\System32\remotecmd-cli.exe` {
		t.Errorf("selectPID(3464) = %+v, %v", got, err)
	}
	if _, err := selectPID(procs, 1); err == nil || !strings.Contains(err.Error(), "2732") || !strings.Contains(err.Error(), "3464") {
		t.Errorf("unknown pid should list the candidates, got %v", err)
	}
}
