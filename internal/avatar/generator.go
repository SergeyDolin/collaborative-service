package avatar

import (
	"crypto/md5"
	"image"
	"image/color"
	"image/png"
	"io"
	"math/rand"
)

const (
	gridW    = 16
	gridH    = 16
	pixScale = 8 // each grid cell → 8×8 px, final image 128×128
)

type scheme struct {
	bg, body, panel, panelDark, accent color.NRGBA
}

var schemes = []scheme{
	// Deep space / cobalt panels
	{bg: c(12, 14, 28), body: c(185, 190, 200), panel: c(45, 105, 210), panelDark: c(28, 68, 160), accent: c(230, 170, 40)},
	// Void / gold panels
	{bg: c(10, 12, 22), body: c(160, 165, 175), panel: c(210, 155, 35), panelDark: c(160, 115, 18), accent: c(100, 205, 255)},
	// Deep ocean / teal panels
	{bg: c(8, 18, 32), body: c(175, 185, 195), panel: c(30, 170, 165), panelDark: c(18, 115, 112), accent: c(255, 100, 85)},
	// Nebula / violet panels
	{bg: c(16, 8, 28), body: c(195, 188, 205), panel: c(130, 65, 210), panelDark: c(90, 40, 155), accent: c(60, 225, 135)},
	// Orbit / jade panels
	{bg: c(8, 22, 14), body: c(178, 192, 178), panel: c(55, 190, 75), panelDark: c(32, 135, 50), accent: c(255, 200, 50)},
}

func c(r, g, b uint8) color.NRGBA { return color.NRGBA{R: r, G: g, B: b, A: 255} }

func darken(col color.NRGBA, d uint8) color.NRGBA {
	sub := func(v uint8) uint8 {
		if v < d {
			return 0
		}
		return v - d
	}
	return color.NRGBA{R: sub(col.R), G: sub(col.G), B: sub(col.B), A: col.A}
}

// satellite shape: 0=bg, 1=body, 2=solar panel, 3=antenna/accent
var shape = [gridH][gridW]uint8{
	{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, // 0
	{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, // 1
	{0, 0, 0, 0, 0, 0, 0, 3, 3, 0, 0, 0, 0, 0, 0, 0}, // 2  antenna tip
	{0, 0, 0, 0, 0, 0, 0, 3, 3, 0, 0, 0, 0, 0, 0, 0}, // 3  antenna shaft
	{2, 2, 2, 2, 2, 0, 0, 1, 1, 0, 0, 2, 2, 2, 2, 2}, // 4  panel + body nub
	{2, 2, 2, 2, 2, 0, 1, 1, 1, 1, 0, 2, 2, 2, 2, 2}, // 5  panel + body shoulder
	{2, 2, 2, 2, 2, 1, 1, 1, 1, 1, 1, 2, 2, 2, 2, 2}, // 6  full row
	{2, 2, 2, 2, 2, 1, 1, 1, 1, 1, 1, 2, 2, 2, 2, 2}, // 7
	{2, 2, 2, 2, 2, 1, 1, 1, 1, 1, 1, 2, 2, 2, 2, 2}, // 8
	{2, 2, 2, 2, 2, 0, 1, 1, 1, 1, 0, 2, 2, 2, 2, 2}, // 9  body shoulder
	{2, 2, 2, 2, 2, 0, 0, 1, 1, 0, 0, 2, 2, 2, 2, 2}, // 10 body nub
	{0, 0, 0, 0, 0, 0, 0, 3, 3, 0, 0, 0, 0, 0, 0, 0}, // 11 antenna shaft
	{0, 0, 0, 0, 0, 0, 0, 3, 3, 0, 0, 0, 0, 0, 0, 0}, // 12 antenna tip
	{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, // 13
	{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, // 14
	{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, // 15
}

// Generate writes a deterministic pixel-art satellite PNG for login to w.
func Generate(login string, w io.Writer) error {
	hash := md5.Sum([]byte(login))
	seed := int64(hash[0]) | int64(hash[1])<<8 | int64(hash[2])<<16 | int64(hash[3])<<24
	rng := rand.New(rand.NewSource(seed))

	sch := schemes[int(hash[4])%len(schemes)]

	img := image.NewNRGBA(image.Rect(0, 0, gridW*pixScale, gridH*pixScale))

	// Background fill
	for y := range gridH * pixScale {
		for x := range gridW * pixScale {
			img.SetNRGBA(x, y, sch.bg)
		}
	}

	// Scatter stars only in background cells
	numStars := 10 + int(hash[5])%10
	for range numStars {
		sx := rng.Intn(gridW * pixScale)
		sy := rng.Intn(gridH * pixScale)
		if shape[sy/pixScale][sx/pixScale] == 0 {
			br := uint8(100 + rng.Intn(156))
			img.SetNRGBA(sx, sy, color.NRGBA{br, br, min8(255, br+20), 200})
		}
	}

	// Render satellite shape
	for gy := range gridH {
		for gx := range gridW {
			cell := shape[gy][gx]
			if cell == 0 {
				continue
			}
			// Per-panel-cell shade variation: deterministic via rng (called in fixed order)
			useDark := cell == 2 && rng.Intn(3) == 0

			for py := range pixScale {
				for px := range pixScale {
					x := gx*pixScale + px
					y := gy*pixScale + py
					var col color.NRGBA
					switch cell {
					case 1: // body
						col = sch.body
						// Subtle cross-hatch texture
						if (px+py)%4 == 0 {
							col = darken(col, 18)
						}
						// Top-left highlight
						if px < 2 && py < 2 {
							col = lighten(col, 20)
						}
					case 2: // solar panel
						if useDark {
							col = sch.panelDark
						} else {
							col = sch.panel
						}
						// Grid cell lines (every 4 px within the panel)
						if px%4 == 0 || py%4 == 0 {
							col = darken(col, 30)
						}
					case 3: // antenna
						col = sch.accent
						// Tip brightening
						if (gy == 2 || gy == 12) && py < 3 {
							col = lighten(col, 25)
						}
					}
					img.SetNRGBA(x, y, col)
				}
			}
		}
	}

	return png.Encode(w, img)
}

func lighten(col color.NRGBA, d uint8) color.NRGBA {
	add := func(v uint8) uint8 {
		if int(v)+int(d) > 255 {
			return 255
		}
		return v + d
	}
	return color.NRGBA{R: add(col.R), G: add(col.G), B: add(col.B), A: col.A}
}

func min8(a, b uint8) uint8 {
	if a < b {
		return a
	}
	return b
}
