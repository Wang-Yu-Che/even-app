package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"github.com/Wang-Yu-Che/even-g2-go/g2"
)

type MiniAppVisibility struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Visible bool   `json:"visible"`
}

func miniAppVisibilityPath() (string, error) {
	path, err := deviceCachePath()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(path), "miniapps.json"), nil
}

func (s *EvenService) loadMiniAppVisibility() {
	path, err := miniAppVisibilityPath()
	if err != nil {
		log.Printf("[miniapps] %v", err)
		return
	}
	orderData, orderErr := os.ReadFile(filepath.Join(filepath.Dir(path), "miniapp-order.json"))
	if orderErr == nil {
		orderErr = json.Unmarshal(orderData, &s.miniAppOrder)
	}
	if orderErr != nil && !errors.Is(orderErr, os.ErrNotExist) {
		log.Printf("[miniapps] load order: %v", orderErr)
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	var hidden map[string]bool
	if err == nil {
		err = json.Unmarshal(data, &hidden)
	}
	if err != nil {
		log.Printf("[miniapps] load visibility: %v", err)
		return
	}
	s.hiddenMiniApps = hidden
}

func (s *EvenService) MiniAppVisibility() []MiniAppVisibility {
	s.mu.Lock()
	defer s.mu.Unlock()

	items := s.orderedMiniAppMenuLocked()
	apps := make([]MiniAppVisibility, 0, len(items))
	for _, item := range items {
		apps = append(apps, MiniAppVisibility{
			ID: item.PackageName, Name: item.Name, Visible: !s.hiddenMiniApps[item.PackageName],
		})
	}
	return apps
}

func (s *EvenService) visibleMiniAppMenuLocked() []g2.MenuItem {
	return slices.DeleteFunc(s.orderedMiniAppMenuLocked(), func(item g2.MenuItem) bool {
		return s.hiddenMiniApps[item.PackageName]
	})
}

func (s *EvenService) SetMiniAppVisible(id string, visible bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !slices.ContainsFunc(s.miniapps.MenuItems(), func(item g2.MenuItem) bool {
		return item.PackageName == id
	}) {
		return fmt.Errorf("未知的 miniapp：%s", id)
	}
	hidden := make(map[string]bool, len(s.hiddenMiniApps))
	maps.Copy(hidden, s.hiddenMiniApps)
	if visible {
		delete(hidden, id)
	} else {
		hidden[id] = true
	}

	path, err := miniAppVisibilityPath()
	if err != nil {
		return err
	}
	data, err := json.Marshal(hidden)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		return err
	}
	s.hiddenMiniApps = hidden
	if s.client != nil && s.client.Status().Ready {
		if err := s.client.SetMenu(s.serviceContext, s.visibleMiniAppMenuLocked()); err != nil {
			return fmt.Errorf("设置已保存，眼镜菜单同步失败：%w", err)
		}
	}
	return nil
}

func (s *EvenService) orderedMiniAppMenuLocked() []g2.MenuItem {
	items := s.miniapps.MenuItems()
	slices.SortStableFunc(items, func(left, right g2.MenuItem) int {
		leftIndex := slices.Index(s.miniAppOrder, left.PackageName)
		rightIndex := slices.Index(s.miniAppOrder, right.PackageName)
		if leftIndex < 0 {
			leftIndex = len(s.miniAppOrder)
		}
		if rightIndex < 0 {
			rightIndex = len(s.miniAppOrder)
		}
		return leftIndex - rightIndex
	})
	return items
}

func (s *EvenService) MoveMiniApp(id string, up bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := s.orderedMiniAppMenuLocked()
	index := slices.IndexFunc(items, func(item g2.MenuItem) bool { return item.PackageName == id })
	if index < 0 {
		return fmt.Errorf("未知的 miniapp：%s", id)
	}
	target := index + 1
	if up {
		target = index - 1
	}
	if target < 0 || target >= len(items) {
		return nil
	}
	items[index], items[target] = items[target], items[index]
	order := make([]string, len(items))
	for index, item := range items {
		order[index] = item.PackageName
	}
	path, err := miniAppVisibilityPath()
	if err != nil {
		return err
	}
	path = filepath.Join(filepath.Dir(path), "miniapp-order.json")
	data, err := json.Marshal(order)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		return err
	}
	s.miniAppOrder = order
	if s.client != nil && s.client.Status().Ready {
		if err := s.client.SetMenu(s.serviceContext, s.visibleMiniAppMenuLocked()); err != nil {
			return fmt.Errorf("排序已保存，眼镜菜单同步失败：%w", err)
		}
	}
	return nil
}
