package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestParseLevels(t *testing.T) {
	got, err := parseLevels("32, 1,8")
	if err != nil {
		t.Fatal(err)
	}
	want := []int{1, 8, 32}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("levels = %v, want %v", got, want)
	}
	for _, raw := range []string{"", "0", "1,1", "x"} {
		if _, err := parseLevels(raw); err == nil {
			t.Errorf("parseLevels(%q) succeeded", raw)
		}
	}
}

func TestPercentile(t *testing.T) {
	values := []time.Duration{time.Millisecond, 2 * time.Millisecond, 3 * time.Millisecond, 4 * time.Millisecond, 5 * time.Millisecond}
	if got := percentile(values, .50); got != 3*time.Millisecond {
		t.Fatalf("p50 = %s", got)
	}
	if got := percentile(values, .95); got != 5*time.Millisecond {
		t.Fatalf("p95 = %s", got)
	}
}

func TestWriteReportUsesCapturedGitState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "baseline.md")
	if err := writeReport(path, "http://127.0.0.1:1", 1, []int{1}, []result{{Concurrency: 1, Requests: 1}}, 0, 0, 0, 0, 0, "v", "release", "sha", "platform", "captured-commit", "clean"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "> 基准代码提交：`captured-commit`") || !strings.Contains(text, "> 工作区：`clean`") {
		t.Fatalf("report does not preserve captured git state: %s", text)
	}
	if !strings.Contains(text, "`G0-02` 基线已冻结") {
		t.Fatalf("clean report does not declare the baseline frozen: %s", text)
	}
}
