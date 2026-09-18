package main

import (
	"slices"
	"testing"
)

func TestTrayPreviewMirrorsGlassesRows(t *testing.T) {
	status := CodexStatus{State: "tool", Rows: []string{lens.Header + "Codex · even-app", "● RUNNING", commandPrompt + "go test ./..."}}
	label, heading, rows := trayPreview(status)
	if label != " RUN" || heading != "眼镜当前显示" {
		t.Fatalf("tray summary = (%q, %q)", label, heading)
	}
	if !slices.Equal(rows, status.Rows) {
		t.Fatalf("tray rows = %v, want %v", rows, status.Rows)
	}
	rows[0] = "changed"
	if status.Rows[0] == "changed" {
		t.Fatal("tray preview aliases the monitor rows")
	}
}

func TestTrayPreviewHandlesEmptyDisplay(t *testing.T) {
	label, heading, rows := trayPreview(CodexStatus{Available: true})
	if label != "" || heading != "眼镜当前无显示" || rows != nil {
		t.Fatalf("empty tray preview = (%q, %q, %v)", label, heading, rows)
	}
}
