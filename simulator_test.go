package main

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestSimulatorBridgePreservesNativePage(t *testing.T) {
	icon := statusIconFor("done", "", 0)
	style := agentPageStyle("done")
	service := &EvenService{}
	service.simulator.setPage("已完成\n更新 3 个文件", style, &icon)
	encoded, err := service.PreviewPage()
	if err != nil {
		t.Fatal(err)
	}
	var page simulatorPage
	if err := json.Unmarshal([]byte(encoded), &page); err != nil {
		t.Fatal(err)
	}
	if page.Style != style || page.Text != "已完成\n更新 3 个文件" || page.Icon == nil || !bytes.Equal(page.Icon.BMP, icon.BMP) {
		t.Fatalf("native page changed in transport: %+v", page)
	}
	service.simulator.setPage(" ", blankAgentPageStyle(), nil)
	encoded, err = service.PreviewPage()
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(encoded), &page); err != nil {
		t.Fatal(err)
	}
	if page.Icon != nil || page.Text != " " {
		t.Fatal("clear left stale content")
	}
}

func TestSimulatorNativeList(t *testing.T) {
	service := &EvenService{}
	style := agentPageStyle("sessions")
	service.simulator.setList([]string{"Codex · 第一个会话", "Codex · 第二个会话"}, style)
	encoded, err := service.PreviewPage()
	if err != nil {
		t.Fatal(err)
	}
	var page simulatorPage
	if err := json.Unmarshal([]byte(encoded), &page); err != nil {
		t.Fatal(err)
	}
	wantItemWidth := style.Width - 2*(style.BorderWidth+style.PaddingLength)
	if page.List == nil || !page.List.SelectBorder || len(page.List.Items) != 2 || page.List.ItemWidth != wantItemWidth || page.Style != style || page.Style.BorderWidth != 1 || page.Icon != nil {
		t.Fatalf("list: %+v", page)
	}
	service.simulator.setPage("detail", agentPageStyle("thinking"), nil)
	encoded, _ = service.PreviewPage()
	page = simulatorPage{}
	_ = json.Unmarshal([]byte(encoded), &page)
	if page.List != nil {
		t.Fatal("detail retained list")
	}
}
