//go:build ignore

// Generates a simple square app icon PNG. Usage: go run mkicon.go Icon.png
package main

import (
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
)

func main() {
	out := "Icon.png"
	if len(os.Args) > 1 {
		out = os.Args[1]
	}
	const s = 1024
	img := image.NewRGBA(image.Rect(0, 0, s, s))

	bgTop := color.RGBA{0x0e, 0x1b, 0x2e, 0xff}
	bgBot := color.RGBA{0x13, 0x33, 0x4d, 0xff}
	ring := color.RGBA{0x3d, 0xd6, 0xb0, 0xff}

	cx, cy := float64(s)/2, float64(s)/2
	for y := 0; y < s; y++ {
		t := float64(y) / float64(s)
		row := color.RGBA{
			R: uint8(float64(bgTop.R) + t*(float64(bgBot.R)-float64(bgTop.R))),
			G: uint8(float64(bgTop.G) + t*(float64(bgBot.G)-float64(bgTop.G))),
			B: uint8(float64(bgTop.B) + t*(float64(bgBot.B)-float64(bgTop.B))),
			A: 0xff,
		}
		for x := 0; x < s; x++ {
			d := math.Hypot(float64(x)-cx, float64(y)-cy)
			switch {
			case d > 300 && d < 340: // outer ring
				img.Set(x, y, ring)
			case d > 150 && d < 185: // inner ring
				img.Set(x, y, ring)
			case math.Abs(float64(x)-cx) < 18 && float64(y) > cy-320 && float64(y) < cy+320:
				img.Set(x, y, ring) // vertical bar through the center
			default:
				img.Set(x, y, row)
			}
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
