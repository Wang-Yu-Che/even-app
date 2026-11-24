package main

import (
	"context"
	"errors"
	"fmt"
	"image"
	"log"
	"time"

	"even-glasses/miniapps"
	codexapp "even-glasses/miniapps/codex"
	gameapp "even-glasses/miniapps/game"
	globeapp "even-glasses/miniapps/globe"
	mediaapp "even-glasses/miniapps/media"

	"github.com/Wang-Yu-Che/even-g2-go/ble"
	"github.com/Wang-Yu-Che/even-g2-go/g2"
	"github.com/Wang-Yu-Che/even-g2-go/protocol"
)

// installedMiniApps is the only installation manifest. Adding or removing one
// entry changes the glasses menu without changing connection or event code.
func installedMiniApps(service *EvenService) *miniapps.Registry {
	service.gameApp = gameapp.New(gameapp.Hooks{
		Display: service.showGameFrame,
		Exit:    service.ClearDisplay,
		Error:   func(err error) { log.Printf("[game] %v", err) },
	})
	apps := []miniapps.MiniApp{
		service.gameApp,
		codexapp.New(codexapp.Hooks{
			Launch: service.openCodexMiniApp,
			Event:  service.handleCodexMiniAppEvent,
		}),
		globeapp.New(globeapp.Hooks{
			Display: func(ctx context.Context, frame *image.Gray) error {
				service.mu.Lock()
				if err := ctx.Err(); err != nil {
					service.mu.Unlock()
					return err
				}
				client := service.client
				if client == nil {
					service.mu.Unlock()
					return errors.New("请先连接 Even G2")
				}
				service.monitor.dismissDisplay()
				service.cancelDisplayTimerLocked()
				service.stopLoadingLocked()
				service.agentPageActive = false
				service.agentIconState = ""
				service.mu.Unlock()
				bmp, err := protocol.EncodeBMP4(globeapp.ImageSize, globeapp.ImageSize, func(x, y int) uint8 {
					return frame.GrayAt(x, y).Y / 17
				})
				if err != nil {
					return err
				}
				icon := g2.StatusIcon{ID: 2, Name: "globe", X: 248, Y: 104, Width: globeapp.ImageSize, Height: globeapp.ImageSize, BMP: bmp}
				return client.ShowTextWithIcon(ctx, globeapp.ContainerName, " ", g2.TextStyle{Width: 576, Height: 288}, icon)
			},
			Exit: func(ctx context.Context) error {
				return service.ClearDisplay()
			},
			Error: func(err error) {
				log.Printf("[globe] display failed: %v", err)
				service.mu.Lock()
				service.recordConnectionErrorLocked(err)
				service.mu.Unlock()
			},
		}),
	}
	if mediaapp.Supported() {
		service.mediaApp = mediaapp.New(mediaapp.Hooks{
			Display: service.showMediaPage,
			Exit:    service.ClearDisplay,
			Error: func(err error) {
				log.Printf("[media] %v", err)
				service.mu.Lock()
				service.recordConnectionErrorLocked(err)
				service.mu.Unlock()
			},
		})
		apps = append(apps, service.mediaApp)
	}
	return miniapps.MustNew(apps...)
}

func (s *EvenService) GameStart() {
	s.gameApp.Launch()
}

func (s *EvenService) GameInput(key string) { s.gameApp.Input(key) }

func (s *EvenService) showGameFrame(ctx context.Context, text string, initial bool) error {
	style := g2.TextStyle{
		X: (576 - gameapp.PageWidth) / 2, Y: (288 - gameapp.PageHeight) / 2,
		Width: gameapp.PageWidth, Height: gameapp.PageHeight,
		BorderWidth: 1, BorderColor: 15, PaddingLength: gameapp.PageInset - 1,
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.client != nil && s.client.Status().Ready {
		if initial {
			s.monitor.dismissDisplay()
			s.cancelDisplayTimerLocked()
			s.stopLoadingLocked()
			s.agentPageActive = false
			s.agentIconState = ""
		}
		var err error
		if initial {
			err = s.client.ShowTextWithStyle(s.serviceContext, gameapp.ContainerName, text, style)
		} else {
			err = s.client.UpdateText(s.serviceContext, gameapp.ContainerName, text)
		}
		if err != nil {
			return err
		}
	}
	s.simulator.setPage(text, style, nil)
	return nil
}

func (s *EvenService) showMediaPage(ctx context.Context, state mediaapp.State, message string, initial bool) error {
	showLyrics := mediaShowsLyrics(state, message)
	clock := mediaClock(state, message)
	divider := mediaBlankDivider
	if showLyrics {
		divider = mediaPageDivider
	}
	rows := state.Lyrics
	if message != "" || !state.HasMedia() {
		rows = [3]string{}
	}
	if bitmapLyrics {
		state.Lyrics = [3]string{}
	}
	message = mediaPageText(state, message, showLyrics)

	return s.withClient(func(client *g2.Client) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if initial {
			s.mediaWriteRetryAt = time.Time{}
		} else if time.Now().Before(s.mediaWriteRetryAt) {
			return mediaapp.ErrDisplayDeferred
		}
		lyric := s.mediaLyricIcon
		lyricChanged := bitmapLyrics && (initial || s.mediaLyricRows != rows)
		lyricDeferred := !initial && lyricChanged && time.Now().Before(s.mediaLyricWriteAt)
		if lyricChanged && !lyricDeferred {
			lyric = mediaLyricsIcon(rows)
			if initial && rows == [3]string{} {
				// A new image container starts blank until its first bitmap arrives.
				lyric.BMP = nil
			}
		}
		textChanged := s.mediaText != message || s.mediaDividerVisible != showLyrics ||
			s.mediaTimeText != [2]string{clock[0].Content, clock[1].Content}
		textDeferred := !initial && lyricChanged && textChanged
		// Artwork only changes with the track; decode and resend it only then.
		cover := s.mediaCoverIcon
		coverChanged := initial || state.ArtworkData != s.mediaCoverKey
		// Pacing for the ~5KB cover, the largest write on this page: wait until
		// a gesture's BLE traffic has drained, send it in a frame of its own,
		// then leave the link idle briefly before the next text write.
		coverDeferred := !initial && coverChanged && time.Since(mediaapp.LastInput()) < mediaCoverGestureQuiet
		if coverChanged && !coverDeferred {
			cover = mediaCoverFor(state.ArtworkData)
		}
		coverSending := coverChanged && !coverDeferred && !initial
		// Text writes are capped at one per mediaTextInterval: the whole ~500B
		// page goes out each time, and title marquee plus clock ticks would
		// otherwise fill the BLE buffer until the glasses exit the page.
		textHeld := !initial && (coverSending || time.Now().Before(s.mediaCoverWriteAt) ||
			(textChanged && time.Since(s.mediaTextWriteAt) < mediaTextInterval))
		// The SDK binds the BLE heartbeat to the write context.
		var err error
		if initial {
			if !client.Status().Ready {
				return errors.New("请先连接 Even G2")
			}
			s.monitor.dismissDisplay()
			s.cancelDisplayTimerLocked()
			s.stopLoadingLocked()
			s.agentPageActive = false
			s.agentIconState = ""
			icons := []g2.StatusIcon{cover, divider}
			if bitmapLyrics {
				icons = append(icons, lyric)
			}
			err = client.ShowTextWithIcons(s.serviceContext, mediaapp.ContainerName, message, mediaPageStyle, icons, clock[:]...)
		} else {
			if !lyricChanged && !textHeld && textChanged {
				if s.mediaDividerVisible != showLyrics {
					if err := client.UpdateStatusIcon(s.serviceContext, divider); err != nil {
						return s.handleMediaWriteErrorLocked(client, fmt.Errorf("media divider: %w", err))
					}
				}
				if s.mediaText != message {
					if err := client.UpdateText(s.serviceContext, mediaapp.ContainerName, message); err != nil {
						return s.handleMediaWriteErrorLocked(client, fmt.Errorf("media text: %w", err))
					}
				}
				for index, item := range clock {
					if s.mediaTimeText[index] != item.Content {
						if err := client.UpdateText(s.serviceContext, item.Name, item.Content, item.ID); err != nil {
							return s.handleMediaWriteErrorLocked(client, fmt.Errorf("media clock: %w", err))
						}
					}
				}
				s.mediaTextWriteAt = time.Now()
			}
			if lyricChanged || textHeld {
				message = s.mediaText
				showLyrics = s.mediaDividerVisible
				for index := range clock {
					clock[index].Content = s.mediaTimeText[index]
				}
			}
		}
		if err == nil && lyricChanged && !initial && !lyricDeferred {
			started := time.Now()
			err = client.UpdateStatusIcon(s.serviceContext, lyric)
			log.Printf("[media-ble] lyric bytes=%d position=%.2fs elapsed=%s ready=%t err=%v",
				len(lyric.BMP), state.ElapsedTime, time.Since(started).Round(time.Millisecond), client.Status().Ready, err)
		}
		if err == nil && coverSending {
			started := time.Now()
			err = client.UpdateStatusIcon(s.serviceContext, cover)
			log.Printf("[media-ble] cover bytes=%d elapsed=%s err=%v", len(cover.BMP), time.Since(started).Round(time.Millisecond), err)
		}
		if err == nil {
			if coverChanged && !coverDeferred {
				s.mediaCoverKey, s.mediaCoverIcon = state.ArtworkData, cover
				if coverSending {
					s.mediaCoverWriteAt = time.Now().Add(mediaCoverSettle)
				}
			}
			if lyricChanged && !lyricDeferred {
				s.mediaLyricRows, s.mediaLyricIcon = rows, lyric
				if len(lyric.BMP) > 0 {
					s.mediaLyricWriteAt = time.Now().Add(time.Second)
				} else {
					s.mediaLyricWriteAt = time.Time{}
				}
			}
			s.mediaText = message
			s.mediaTimeText = [2]string{clock[0].Content, clock[1].Content}
			s.mediaDividerVisible = showLyrics
			s.simulator.setMediaPage(message, cover, lyric, showLyrics, clock)
		}
		if err == nil && (lyricDeferred || textDeferred || coverDeferred || textHeld) {
			return mediaapp.ErrDisplayDeferred
		}
		if !initial {
			return s.handleMediaWriteErrorLocked(client, err)
		}
		return err
	})
}

const (
	// mediaCoverGestureQuiet is how long after a gesture the cover waits.
	mediaCoverGestureQuiet = 1500 * time.Millisecond
	// mediaCoverSettle keeps text writes off the link right after a cover.
	mediaCoverSettle = time.Second
	// mediaTextInterval is the minimum gap between page text writes.
	mediaTextInterval = 750 * time.Millisecond
)

func (s *EvenService) handleMediaWriteErrorLocked(client *g2.Client, err error) error {
	if !errors.Is(err, ble.ErrWriteBufferTimeout) || !client.Status().Ready {
		return err
	}
	s.mediaWriteRetryAt = time.Now().Add(2 * time.Second)
	log.Printf("[media-ble] write paused after buffer timeout: %v", err)
	return mediaapp.ErrDisplayDeferred
}

const (
	codexMenuPackage    = codexapp.PackageName
	streamContainerName = codexapp.ContainerName
)
