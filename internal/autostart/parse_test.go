package autostart

import (
	"reflect"
	"testing"
)

func TestParseSystemdUnitPreservesExecutableWithSpaces(t *testing.T) {
	executable, arguments, err := parseSystemdUnit("[Service]\nExecStart=\"/home/test/AI Gateway/ai-gateway\" serve\n")
	if err != nil {
		t.Fatal(err)
	}
	if executable != "/home/test/AI Gateway/ai-gateway" || !reflect.DeepEqual(arguments, []string{"serve"}) {
		t.Fatalf("executable=%q arguments=%v", executable, arguments)
	}
}

func TestParseLaunchdArgumentsFindsExactProgramArray(t *testing.T) {
	plist := []byte(`<?xml version="1.0"?><plist><dict>
<key>Label</key><string>local.ai-gateway.gateway</string>
<key>ProgramArguments</key><array><string>/Users/test/AI Gateway/ai-gateway</string><string>serve</string></array>
<key>RunAtLoad</key><true/>
</dict></plist>`)
	arguments, err := parseLaunchdArguments(plist)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/Users/test/AI Gateway/ai-gateway", "serve"}
	if !reflect.DeepEqual(arguments, want) {
		t.Fatalf("arguments=%v, want %v", arguments, want)
	}
}
