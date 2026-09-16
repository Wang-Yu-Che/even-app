package main

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/Wang-Yu-Che/even-g2-go/ble"
	"github.com/Wang-Yu-Che/even-g2-go/g2"
)

type cachedDevice struct {
	LeftAddress  string `json:"leftAddress"`
	LeftName     string `json:"leftName"`
	RightAddress string `json:"rightAddress"`
	RightName    string `json:"rightName"`
}

func deviceCachePath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "even-app", "device.json"), nil
}

func loadCachedDevice(path string) (g2.Device, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return g2.Device{}, err
	}
	var cached cachedDevice
	if err := json.Unmarshal(data, &cached); err != nil {
		return g2.Device{}, err
	}
	if cached.LeftAddress == "" || cached.RightAddress == "" {
		return g2.Device{}, os.ErrInvalid
	}
	return g2.NewDevice(
		ble.ScanResult{Arm: ble.Left, Address: cached.LeftAddress, Name: cached.LeftName},
		ble.ScanResult{Arm: ble.Right, Address: cached.RightAddress, Name: cached.RightName},
	)
}

func saveCachedDevice(path string, device g2.Device) error {
	data, err := json.Marshal(cachedDevice{
		LeftAddress: device.Left.Address, LeftName: device.Left.Name,
		RightAddress: device.Right.Address, RightName: device.Right.Name,
	})
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
	return os.Rename(temporary, path)
}
