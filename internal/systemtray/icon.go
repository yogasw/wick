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

func wickIcon() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 32, 32))
	bg := color.RGBA{0x1d, 0x7d, 0x4f, 0xff}
	draw.Draw(img, img.Bounds(), &image.Uniform{bg}, image.Point{}, draw.Src)

	fg := color.RGBA{0xff, 0xff, 0xff, 0xff}
	for _, s := range [][4]int{
		{6, 7, 11, 25},
		{11, 25, 16, 13},
		{16, 13, 21, 25},
		{21, 25, 26, 7},
	} {
		drawLine(img, s[0], s[1], s[2], s[3], fg)
	}

	var buf bytes.Buffer
	if runtime.GOOS == "windows" {
		_ = ico.Encode(&buf, img)
	} else {
		_ = png.Encode(&buf, img)
	}
	return buf.Bytes()
}

func drawLine(img *image.RGBA, x0, y0, x1, y1 int, c color.Color) {
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
	for {
		for ox := 0; ox < 2; ox++ {
			for oy := 0; oy < 2; oy++ {
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
