//go:build !darwin || !cgo

package main

func coverSubjectMask([]byte, int, int) []float32 { return nil }
