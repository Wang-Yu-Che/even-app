package main

import (
	"testing"

	"even-glasses/miniapps"
	codexapp "even-glasses/miniapps/codex"
	gameapp "even-glasses/miniapps/game"
	globeapp "even-glasses/miniapps/globe"
)

func TestMiniAppMenuOrder(t *testing.T) {
	service := &EvenService{
		miniapps: miniapps.MustNew(
			gameapp.New(gameapp.Hooks{}),
			codexapp.New(codexapp.Hooks{}),
			globeapp.New(globeapp.Hooks{}),
		),
		miniAppOrder:   []string{codexapp.PackageName, gameapp.PackageName},
		hiddenMiniApps: map[string]bool{gameapp.PackageName: true},
	}
	apps := service.MiniAppVisibility()
	if len(apps) != 3 || apps[0].ID != codexapp.PackageName || apps[1].ID != gameapp.PackageName || apps[2].ID != globeapp.PackageName {
		t.Fatalf("unexpected order: %+v", apps)
	}
	if apps[1].Visible {
		t.Fatal("hidden app became visible")
	}
	menu := service.visibleMiniAppMenuLocked()
	if len(menu) != 2 || menu[0].PackageName != codexapp.PackageName || menu[1].PackageName != globeapp.PackageName {
		t.Fatalf("unexpected glasses menu: %+v", menu)
	}
}
