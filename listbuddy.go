package main

import (
	"embed"
	"fmt"
	"image/color"
	"image/png"
	"sync"

	"github.com/Wang-Yu-Che/even-g2-go/g2"
	"github.com/Wang-Yu-Che/even-g2-go/protocol"
)

const (
	listBuddyID   = 2
	listBuddyName = "list-buddy"
	listBuddyX    = 450
	listBuddyY    = 112
	listBuddyW    = 80
	listBuddyH    = 96
)

const (
	listBuddyQingzhu   = "qingzhu"
	listBuddyXiaojiaqi = "xiaojiaqi"
)

//go:embed assets/pets/*/frames/*.png
var listBuddyAssets embed.FS

var (
	listBuddyFramesOnce sync.Once
	listBuddyPetFrames  map[string][]g2.StatusIcon
)

func validListBuddyMode(mode string) bool {
	return mode == listBuddyQingzhu || mode == listBuddyXiaojiaqi
}

func listBuddyAnimation(mode, _ string) []g2.StatusIcon {
	listBuddyFramesOnce.Do(loadListBuddyFrames)
	if frames := listBuddyPetFrames[mode]; len(frames) > 0 {
		return frames
	}
	return listBuddyPetFrames[listBuddyQingzhu]
}

func loadListBuddyFrames() {
	listBuddyPetFrames = map[string][]g2.StatusIcon{}
	for _, pet := range []string{listBuddyQingzhu, listBuddyXiaojiaqi} {
		for frame := 0; frame < 3; frame++ {
			file, err := listBuddyAssets.Open(fmt.Sprintf("assets/pets/%s/frames/%d.png", pet, frame))
			if err != nil {
				break
			}
			image, err := png.Decode(file)
			file.Close()
			if err != nil || image.Bounds().Dx() != listBuddyW || image.Bounds().Dy() != listBuddyH {
				break
			}
			bmp, _ := protocol.EncodeBMP4(listBuddyW, listBuddyH, func(x, y int) uint8 {
				gray := color.GrayModel.Convert(image.At(image.Bounds().Min.X+x, image.Bounds().Min.Y+y)).(color.Gray)
				return uint8((uint16(gray.Y)*15 + 127) / 255)
			})
			listBuddyPetFrames[pet] = append(listBuddyPetFrames[pet], g2.StatusIcon{
				ID: listBuddyID, Name: listBuddyName, X: listBuddyX, Y: listBuddyY,
				Width: listBuddyW, Height: listBuddyH, BMP: bmp,
			})
		}
	}
}
