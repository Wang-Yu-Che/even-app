package main

import (
	"path/filepath"
	"testing"

	"github.com/Wang-Yu-Che/even-g2-go/ble"
	"github.com/Wang-Yu-Che/even-g2-go/g2"
)

func TestDeviceCacheRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "device.json")
	device, err := g2.NewDevice(
		ble.ScanResult{Arm: ble.Left, Address: "left-id", Name: "G2_L_123"},
		ble.ScanResult{Arm: ble.Right, Address: "right-id", Name: "G2_R_123"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := saveCachedDevice(path, device); err != nil {
		t.Fatalf("saveCachedDevice() error = %v", err)
	}
	loaded, err := loadCachedDevice(path)
	if err != nil {
		t.Fatalf("loadCachedDevice() error = %v", err)
	}
	if loaded.Left.Address != device.Left.Address || loaded.Right.Address != device.Right.Address {
		t.Fatalf("loadCachedDevice() = %#v, want %#v", loaded, device)
	}
	if loaded.Left.Arm != ble.Left || loaded.Right.Arm != ble.Right {
		t.Fatalf("cached arms = %v/%v, want LEFT/RIGHT", loaded.Left.Arm, loaded.Right.Arm)
	}
}

func TestLoadCachedDeviceRejectsIncompletePair(t *testing.T) {
	path := filepath.Join(t.TempDir(), "device.json")
	if err := saveCachedDevice(path, g2.Device{}); err != nil {
		t.Fatal(err)
	}
	if _, err := loadCachedDevice(path); err == nil {
		t.Fatal("loadCachedDevice() accepted an incomplete pair")
	}
}
