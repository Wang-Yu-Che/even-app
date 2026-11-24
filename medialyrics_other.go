//go:build !darwin || !cgo

package main

import "github.com/Wang-Yu-Che/even-g2-go/g2"

const bitmapLyrics = false

func mediaLyricsIcon([3]string) g2.StatusIcon { return g2.StatusIcon{} }
