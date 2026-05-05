//go:build !headless

package systemtray

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"runtime"

	ico "github.com/sergeymakinen/go-ico"
)

// wickIcon renders the brand W with a state-specific corner badge,
// Defender-style:
//
//	stopped (both off) : gray bg + dim W (no badge)
//	server only        : blue bg + W + server-bars badge
//	worker only        : orange bg + W + gear badge
//	both               : green bg + W + green-check badge
//
// 64×64 canvas. Background color is the primary signal at 16-px tray
// scale; the badge becomes legible at larger DPI.
func wickIcon(serverRunning, workerRunning bool) []byte {
	const size = 64
	img := image.NewRGBA(image.Rect(0, 0, size, size))

	white := color.RGBA{0xff, 0xff, 0xff, 0xff}
	dim := color.RGBA{0xc8, 0xc7, 0xc1, 0xff}

	var bg color.RGBA
	switch {
	case serverRunning && workerRunning:
		bg = color.RGBA{0x1d, 0x7d, 0x4f, 0xff}
	case serverRunning:
		bg = color.RGBA{0x18, 0x5f, 0xa5, 0xff}
	case workerRunning:
		bg = color.RGBA{0xef, 0x9f, 0x27, 0xff}
	default:
		bg = color.RGBA{0x88, 0x87, 0x80, 0xff}
	}
	fillBG(img, bg)

	wColor := white
	if !serverRunning && !workerRunning {
		wColor = dim
	}
	drawW(img, wColor)

	switch {
	case serverRunning && workerRunning:
		drawCheckBadge(img)
	case serverRunning:
		drawServerBadge(img)
	case workerRunning:
		drawGearBadge(img)
	}

	var buf bytes.Buffer
	if runtime.GOOS == "windows" {
		_ = ico.Encode(&buf, img)
	} else {
		_ = png.Encode(&buf, img)
	}
	return buf.Bytes()
}

// Badge: white disk in bottom-right corner with a state glyph drawn
// on top. Big enough (~1/3 of canvas) so the corner is recognizable
// even when tray scales to 16/24px.
const (
	badgeCx     = 48
	badgeCy     = 48
	badgeRadius = 16
)

func drawCheckBadge(img *image.RGBA) {
	white := color.RGBA{0xff, 0xff, 0xff, 0xff}
	green := color.RGBA{0x1d, 0x7d, 0x4f, 0xff}
	fillCircle(img, badgeCx, badgeCy, badgeRadius, white)
	// ✓ — short stroke down-right then long stroke up-right
	drawLine(img, badgeCx-8, badgeCy, badgeCx-3, badgeCy+5, 4, green)
	drawLine(img, badgeCx-3, badgeCy+5, badgeCx+9, badgeCy-7, 4, green)
}

func drawServerBadge(img *image.RGBA) {
	white := color.RGBA{0xff, 0xff, 0xff, 0xff}
	blue := color.RGBA{0x18, 0x5f, 0xa5, 0xff}
	fillCircle(img, badgeCx, badgeCy, badgeRadius, white)
	// 3 stacked bars
	barW, barH, gap := 16, 3, 3
	startY := badgeCy - (3*barH+2*gap)/2
	for i := 0; i < 3; i++ {
		fillRect(img, badgeCx-barW/2, startY+i*(barH+gap), barW, barH, blue)
	}
}

func drawGearBadge(img *image.RGBA) {
	white := color.RGBA{0xff, 0xff, 0xff, 0xff}
	orange := color.RGBA{0xef, 0x9f, 0x27, 0xff}
	fillCircle(img, badgeCx, badgeCy, badgeRadius, white)
	// Solid ring with center hole
	for x := badgeCx - 11; x <= badgeCx+11; x++ {
		for y := badgeCy - 11; y <= badgeCy+11; y++ {
			dx, dy := x-badgeCx, y-badgeCy
			d2 := dx*dx + dy*dy
			if d2 <= 11*11 && d2 >= 5*5 {
				img.Set(x, y, orange)
			}
		}
	}
	// 4 cardinal teeth
	for _, t := range [][4]int{
		{badgeCx - 2, badgeCy - 14, 4, 4},
		{badgeCx - 2, badgeCy + 10, 4, 4},
		{badgeCx - 14, badgeCy - 2, 4, 4},
		{badgeCx + 10, badgeCy - 2, 4, 4},
	} {
		fillRect(img, t[0], t[1], t[2], t[3], orange)
	}
}

func fillCircle(img *image.RGBA, cx, cy, r int, c color.Color) {
	for x := cx - r; x <= cx+r; x++ {
		for y := cy - r; y <= cy+r; y++ {
			dx, dy := x-cx, y-cy
			if dx*dx+dy*dy <= r*r {
				img.Set(x, y, c)
			}
		}
	}
}


func fillBG(img *image.RGBA, c color.RGBA) {
	draw.Draw(img, img.Bounds(), &image.Uniform{c}, image.Point{}, draw.Src)
}

func drawW(img *image.RGBA, c color.Color) {
	const stroke = 8
	for _, s := range [][4]int{
		{3, 6, 21, 58},
		{21, 58, 32, 24},
		{32, 24, 43, 58},
		{43, 58, 61, 6},
	} {
		drawLine(img, s[0], s[1], s[2], s[3], stroke, c)
	}
}

func fillRect(img *image.RGBA, x, y, w, h int, c color.Color) {
	for px := x; px < x+w; px++ {
		for py := y; py < y+h; py++ {
			img.Set(px, py, c)
		}
	}
}

func drawLine(img *image.RGBA, x0, y0, x1, y1, thickness int, c color.Color) {
	dx := abs(x1 - x0)
	dy := -abs(y1 - y0)
	sx, sy := 1, 1
	if x0 >= x1 {
		sx = -1
	}
	if y0 >= y1 {
		sy = -1
	}
	err := dx + dy
	half := thickness / 2
	for {
		for ox := -half; ox <= half; ox++ {
			for oy := -half; oy <= half; oy++ {
				img.Set(x0+ox, y0+oy, c)
			}
		}
		if x0 == x1 && y0 == y1 {
			break
		}
		e2 := 2 * err
		if e2 >= dy {
			err += dy
			x0 += sx
		}
		if e2 <= dx {
			err += dx
			y0 += sy
		}
	}
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
