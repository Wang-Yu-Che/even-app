package main

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/Wang-Yu-Che/even-g2-go/protocol"
)

const imuSampleLimit = 120

type IMUSample struct {
	Millis float64 `json:"millis"`
	X      float32 `json:"x"`
	Y      float32 `json:"y"`
	Z      float32 `json:"z"`
}

type IMUDiagnostics struct {
	Active  bool        `json:"active"`
	Samples []IMUSample `json:"samples"`
}

func (s *EvenService) SetIMUDiagnostics(enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.imuActive == enabled {
		return nil
	}
	if s.client == nil || !s.client.Status().Ready {
		return errors.New("请先连接 Even G2")
	}
	started := time.Now()
	ctx, cancel := context.WithTimeout(s.serviceContext, 5*time.Second)
	defer cancel()
	if err := s.client.SetIMU(ctx, enabled, 500); err != nil {
		return err
	}
	s.imuActive = enabled
	if enabled {
		s.imuStartedAt = started
		s.imuSamples = s.imuSamples[:0]
	}
	return nil
}

func (s *EvenService) GetIMUDiagnostics() IMUDiagnostics {
	s.mu.Lock()
	defer s.mu.Unlock()
	return IMUDiagnostics{Active: s.imuActive, Samples: slices.Clone(s.imuSamples)}
}

func (s *EvenService) recordIMUSample(event protocol.EvenHubEvent, received time.Time) {
	sample := IMUSample{
		Millis: float64(received.Sub(s.imuStartedAt)) / float64(time.Millisecond),
		X:      event.IMUX, Y: event.IMUY, Z: event.IMUZ,
	}
	if len(s.imuSamples) == imuSampleLimit {
		copy(s.imuSamples, s.imuSamples[1:])
		s.imuSamples[imuSampleLimit-1] = sample
		return
	}
	s.imuSamples = append(s.imuSamples, sample)
}
