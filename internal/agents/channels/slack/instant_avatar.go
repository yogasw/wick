// Package slack — instant_avatar.go: a PNG of a Team agent's avatar for
// Slack's icon_url. Drawn with the standard library only: the agent's
// colour in its shape with two eyes, close enough to the web avatar to be
// recognised at Slack's 36–72 px.

package slack

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
	"strconv"
	"strings"
)

const avatarSize = 128

// RenderAvatarPNG draws shape ("circle", "squircle", "triangle",
// "diamond"; anything else is a circle) filled with hexColor.
func RenderAvatarPNG(shape, hexColor string) []byte {
	fill := parseHexColor(hexColor)
	img := image.NewNRGBA(image.Rect(0, 0, avatarSize, avatarSize))
	const n = avatarSize
	c := float64(n) / 2
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			px, py := float64(x)+0.5, float64(y)+0.5
			if insideShape(shape, px, py, c) {
				img.SetNRGBA(x, y, fill)
			}
		}
	}
	// Eyes: white with a dark pupil, a little above the centre.
	eyeY := c - 4
	if shape == "triangle" {
		eyeY = c + 14
	}
	for _, ex := range []float64{c - 18, c + 18} {
		disc(img, ex, eyeY, 11, color.NRGBA{255, 255, 255, 255})
		disc(img, ex+2, eyeY+1, 5, color.NRGBA{30, 30, 40, 255})
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

func insideShape(shape string, x, y, c float64) bool {
	dx, dy := x-c, y-c
	r := c - 4
	switch shape {
	case "squircle":
		// Superellipse |x|^4 + |y|^4 <= r^4.
		return math.Pow(math.Abs(dx)/r, 4)+math.Pow(math.Abs(dy)/r, 4) <= 1
	case "diamond":
		return math.Abs(dx)+math.Abs(dy) <= r
	case "triangle":
		// Apex up, base at the bottom.
		top, bottom := 6.0, 2*c-10
		if y < top || y > bottom {
			return false
		}
		half := (y - top) / (bottom - top) * r
		return math.Abs(dx) <= half
	default:
		return dx*dx+dy*dy <= r*r
	}
}

func disc(img *image.NRGBA, cx, cy, r float64, col color.NRGBA) {
	for y := int(cy - r); y <= int(cy+r); y++ {
		for x := int(cx - r); x <= int(cx+r); x++ {
			dx, dy := float64(x)+0.5-cx, float64(y)+0.5-cy
			if dx*dx+dy*dy <= r*r && image.Pt(x, y).In(img.Rect) {
				img.SetNRGBA(x, y, col)
			}
		}
	}
}

// parseHexColor reads #rgb / #rrggbb; anything else is wick's default blue.
func parseHexColor(s string) color.NRGBA {
	def := color.NRGBA{0x00, 0x44, 0x92, 0xff}
	h := strings.TrimPrefix(strings.TrimSpace(s), "#")
	if len(h) == 3 {
		h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
	}
	if len(h) != 6 {
		return def
	}
	v, err := strconv.ParseUint(h, 16, 32)
	if err != nil {
		return def
	}
	return color.NRGBA{uint8(v >> 16), uint8(v >> 8), uint8(v), 0xff}
}
