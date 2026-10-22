//go:build ignore

// Generates the Orbi-VPN app icon: a near-black rounded square with an
// off-white ring, on a white canvas. Usage: go run mkicon.go Icon.png
package main

import (
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
)

const (
	size   = 1024
	margin = 0.0    // full-bleed: no border, corners are transparent
	radius = 230.0  // corner radius of the rounded square (~macOS squircle)
	ringR  = 330.0  // ring outer radius
	ringW  = 44.0   // ring thickness
)

var (
	clear = color.NRGBA{0x00, 0x00, 0x00, 0x00} // transparent outside the square
	black = color.NRGBA{0x0a, 0x0a, 0x0f, 0xff}
	ring  = color.NRGBA{0xfd, 0xf2, 0xf2, 0xff}
)

func inRoundRect(px, py, x0, y0, x1, y1, r float64) bool {
	if px < x0 || px > x1 || py < y0 || py > y1 {
		return false
	}
	cx, cy := x0+r, y0+r
	if px < cx && py < cy {
		return math.Hypot(px-cx, py-cy) <= r
	}
	cx, cy = x1-r, y0+r
	if px > cx && py < cy {
		return math.Hypot(px-cx, py-cy) <= r
	}
	cx, cy = x0+r, y1-r
	if px < cx && py > cy {
		return math.Hypot(px-cx, py-cy) <= r
	}
	cx, cy = x1-r, y1-r
	if px > cx && py > cy {
		return math.Hypot(px-cx, py-cy) <= r
	}
	return true
}

func main() {
	out := "Icon.png"
	if len(os.Args) > 1 {
		out = os.Args[1]
	}
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	cx, cy := float64(size)/2, float64(size)/2
	x0, y0, x1, y1 := margin, margin, float64(size)-margin, float64(size)-margin

	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			px, py := float64(x)+0.5, float64(y)+0.5
			c := clear
			if inRoundRect(px, py, x0, y0, x1, y1, radius) {
				c = black
				if d := math.Hypot(px-cx, py-cy); d <= ringR && d >= ringR-ringW {
					c = ring
				}
			}
			img.SetNRGBA(x, y, c)
		}
	}

	f, err := os.Create(out)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		panic(err)
	}
}
