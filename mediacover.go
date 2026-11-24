package main

import (
	"bytes"
	"encoding/base64"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"slices"

	"github.com/Wang-Yu-Che/even-g2-go/g2"
)

// mediaCoverFor turns MediaRemote's base64 artwork into the lens cover bitmap,
// falling back to the placeholder when there is no artwork or it won't decode.
func mediaCoverFor(artwork string) g2.StatusIcon {
	if artwork == "" {
		return mediaPageCover
	}
	data, err := base64.StdEncoding.DecodeString(artwork)
	if err != nil {
		return mediaPageCover
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return mediaPageCover
	}
	b := img.Bounds()
	levels := mediaCoverLevels(img, coverSubjectMask(data, b.Dx(), b.Dy()))
	return mediaBitmap(mediaPageCover.ID, mediaPageCover.Name, mediaPageCover.X, mediaPageCover.Y,
		mediaCoverSize, mediaCoverSize, func(x, y int) uint8 { return levels[y*mediaCoverSize+x] })
}

// mediaCoverLevels prepares a square cover for the 16-level monochrome lens.
// The waveguide is bright and covers are often small (NetEase sends 100px),
// so: supersampled bilinear resize, contrast stretch, mid-tones pushed down
// (gamma 1.4) and peak brightness capped below full so the image doesn't
// glare, then an unsharp mask so edges and title text stay crisp.
//
// When subject is a mask (image-sized, 0..1) the subject keeps full contrast
// and everything else is dimmed to a faint backdrop; without one the whole
// cover is dimmed a little instead.
func mediaCoverLevels(img image.Image, subject []float32) []uint8 {
	const (
		n            = mediaCoverSize
		gamma        = 1.4
		maxLevel     = 12
		sharpen      = 1.0
		subjectLevel = 0.85 // subject brightness, kept below full so it does not glare
		backdrop     = 0.25 // brightness kept outside the subject
		noMask       = 0.75 // overall brightness when no subject is found
	)
	b := img.Bounds()
	side := min(b.Dx(), b.Dy())
	x0, y0 := b.Min.X+(b.Dx()-side)/2, b.Min.Y+(b.Dy()-side)/2
	lum := make([]float64, side*side)
	weight := make([]float64, side*side)
	for y := range side {
		for x := range side {
			r, g, bl, _ := img.At(x0+x, y0+y).RGBA()
			lum[y*side+x] = (0.2126*float64(r) + 0.7152*float64(g) + 0.0722*float64(bl)) / 0xffff
			weight[y*side+x] = noMask
			if subject != nil {
				m := float64(subject[(y0-b.Min.Y+y)*b.Dx()+x0-b.Min.X+x])
				weight[y*side+x] = backdrop + (subjectLevel-backdrop)*m
			}
		}
	}
	at := func(x, y float64) float64 {
		x, y = min(max(x, 0), float64(side-1)), min(max(y, 0), float64(side-1))
		ix, iy := int(x), int(y)
		jx, jy := min(ix+1, side-1), min(iy+1, side-1)
		fx, fy := x-float64(ix), y-float64(iy)
		return lum[iy*side+ix]*(1-fx)*(1-fy) + lum[iy*side+jx]*fx*(1-fy) +
			lum[jy*side+ix]*(1-fx)*fy + lum[jy*side+jx]*fx*fy
	}

	// 4×4 supersampling per output pixel works for both up- and downscaling.
	luma := make([]float64, n*n)
	scale := float64(side) / n
	for y := range n {
		for x := range n {
			sum := 0.0
			for sy := range 4 {
				for sx := range 4 {
					sum += at((float64(x)+(float64(sx)+0.5)/4)*scale-0.5, (float64(y)+(float64(sy)+0.5)/4)*scale-0.5)
				}
			}
			luma[y*n+x] = sum / 16
		}
	}

	sorted := slices.Clone(luma)
	slices.Sort(sorted)
	lo, hi := sorted[len(sorted)/100], sorted[len(sorted)*99/100]
	if hi-lo < 0.05 {
		lo, hi = 0, 1
	}
	for i, v := range luma {
		w := weight[(i/n*side/n)*side+i%n*side/n]
		luma[i] = math.Pow(min(1, max(0, (v-lo)/(hi-lo))), gamma) * w
	}

	out := make([]uint8, n*n)
	for y := range n {
		for x := range n {
			sum, k := 0.0, 0.0
			for dy := -1; dy <= 1; dy++ {
				for dx := -1; dx <= 1; dx++ {
					if xx, yy := x+dx, y+dy; xx >= 0 && xx < n && yy >= 0 && yy < n {
						sum += luma[yy*n+xx]
						k++
					}
				}
			}
			v := luma[y*n+x]
			v += sharpen * (v - sum/k)
			out[y*n+x] = uint8(math.Round(min(1, max(0, v)) * maxLevel))
		}
	}
	// A dim frame keeps dark covers' bounds visible, matching the placeholder.
	for i := range n {
		out[i], out[(n-1)*n+i], out[i*n], out[i*n+n-1] = 7, 7, 7, 7
	}
	return out
}
