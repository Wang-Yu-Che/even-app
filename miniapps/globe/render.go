package globe

import (
	"bytes"
	_ "embed"
	"image"
	"image/jpeg"
	"math"
)

//go:embed assets/earth-source.jpg
var earthJPEG []byte

type textureMap struct {
	gray *image.Gray
	land []bool
}

var earthTexture = decodeEarthTexture()

const ImageSize = 80

func decodeEarthTexture() textureMap {
	source, err := jpeg.Decode(bytes.NewReader(earthJPEG))
	if err != nil {
		panic("invalid embedded Earth texture: " + err.Error())
	}
	gray := image.NewGray(source.Bounds())
	land := make([]bool, gray.Rect.Dx()*gray.Rect.Dy())
	for y := 0; y < gray.Rect.Dy(); y++ {
		for x := 0; x < gray.Rect.Dx(); x++ {
			r, g, b, _ := source.At(x, y).RGBA()
			red, green, blue := int(r>>8), int(g>>8), int(b>>8)
			luminance := (299*red + 587*green + 114*blue) / 1000
			// Color carries the coastline contrast lost in monochrome: blue sea
			// becomes darker while terrain keeps its original relief.
			isOcean := blue > red+25 && blue > green+10
			if isOcean {
				luminance -= 25
			} else {
				luminance += 12
			}
			gray.Pix[y*gray.Stride+x] = uint8(max(0, min(255, luminance)))
			land[y*gray.Rect.Dx()+x] = !isOcean
		}
	}
	return textureMap{gray: gray, land: land}
}

// Render draws a low-bandwidth globe as coastline and graticule line art.
func Render(rotation float64) *image.Gray {
	const center, radius = ImageSize / 2, 38
	img := image.NewGray(image.Rect(0, 0, ImageSize, ImageSize))
	textureWidth, textureHeight := earthTexture.gray.Rect.Dx(), earthTexture.gray.Rect.Dy()
	for y := center - radius; y <= center+radius; y++ {
		for x := center - radius; x <= center+radius; x++ {
			dx := float64(x-center) / radius
			dy := float64(y-center) / radius
			r2 := dx*dx + dy*dy
			if r2 > 1 {
				continue
			}
			z := math.Sqrt(1 - r2)
			latitude := math.Asin(-dy)
			longitude := math.Atan2(dx, z) + rotation
			u := math.Mod(longitude/(2*math.Pi)+0.5, 1)
			if u < 0 {
				u++
			}
			v := 0.5 - latitude/math.Pi
			textureX := int(u * float64(textureWidth))
			textureY := min(textureHeight-1, int(v*float64(textureHeight)))
			latitudeDegrees := latitude * 180 / math.Pi
			longitudeDegrees := longitude * 180 / math.Pi
			shade := 0.0
			light := math.Max(0, -0.35*dx-0.2*dy+0.91*z)
			if earthTexture.isCoast(textureX, textureY) {
				shade = 15
			} else if math.Abs(math.Remainder(latitudeDegrees, 30)) < 1.1 || math.Abs(math.Remainder(longitudeDegrees, 30)) < 1.1 {
				shade = 5 + 4*light
			}
			if r2 > 0.94 {
				shade = 15
			}
			img.Pix[y*img.Stride+x] = uint8(math.Round(math.Min(15, shade)) * 17)
		}
	}
	return img
}

func (texture textureMap) isLand(x, y int) bool {
	width, height := texture.gray.Rect.Dx(), texture.gray.Rect.Dy()
	x = (x%width + width) % width
	y = max(0, min(height-1, y))
	return texture.land[y*width+x]
}

func (texture textureMap) isCoast(x, y int) bool {
	land := texture.isLand(x, y)
	const distance = 8
	return texture.isLand(x-distance, y) != land ||
		texture.isLand(x+distance, y) != land ||
		texture.isLand(x, y-distance) != land ||
		texture.isLand(x, y+distance) != land
}
