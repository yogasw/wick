package team

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"math"
	"testing"

	"github.com/yogasw/wick/internal/entity"
)

func gradientPNG(t *testing.T, w, h int, alpha bool) []byte {
	t.Helper()
	m := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			a := uint8(255)
			if alpha && x < w/2 {
				a = 128
			}
			m.Set(x, y, color.NRGBA{uint8(x * 255 / w), uint8(y * 255 / h), 200, a})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, m); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// withPNGText inserts a tEXt chunk (metadata) right after IHDR.
func withPNGText(raw []byte, text string) []byte {
	chunk := func(typ string, data []byte) []byte {
		var b bytes.Buffer
		_ = binary.Write(&b, binary.BigEndian, uint32(len(data)))
		b.WriteString(typ)
		b.Write(data)
		_ = binary.Write(&b, binary.BigEndian, crc32.ChecksumIEEE(append([]byte(typ), data...)))
		return b.Bytes()
	}
	ihdrEnd := 8 + 8 + 13 + 4
	out := append([]byte{}, raw[:ihdrEnd]...)
	out = append(out, chunk("tEXt", []byte("Comment\x00"+text))...)
	return append(out, raw[ihdrEnd:]...)
}

func TestProcessAvatarImageValidPNG(t *testing.T) {
	raw := withPNGText(gradientPNG(t, 800, 400, true), "secret-exif-like-metadata")
	img, err := ProcessAvatarImage(raw)
	if err != nil {
		t.Fatal(err)
	}
	if img.ContentType != "image/png" || img.Width != 320 || img.Height != 160 {
		t.Fatalf("got %s %dx%d, want image/png 320x160", img.ContentType, img.Width, img.Height)
	}
	if bytes.Contains(img.Data, []byte("secret-exif-like-metadata")) {
		t.Fatal("metadata survived the re-encode")
	}
	if len(img.Version) != 16 || len(img.Data) > AvatarImageMaxOut {
		t.Fatalf("version %q size %d", img.Version, len(img.Data))
	}
}

// A polyglot (a valid PNG with an HTML/script payload appended) comes out
// as fresh pixels only: nothing after the image data survives.
func TestProcessAvatarImagePolyglotStripped(t *testing.T) {
	raw := append(gradientPNG(t, 32, 32, true), []byte("<html><script>alert(1)</script></html>")...)
	img, err := ProcessAvatarImage(raw)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(img.Data, []byte("<script>")) || !bytes.HasPrefix(img.Data, []byte("\x89PNG")) {
		t.Fatal("polyglot payload survived the re-encode")
	}
}

func TestProcessAvatarImageOpaqueBecomesJPEG(t *testing.T) {
	m := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for i := range m.Pix {
		m.Pix[i] = 0xff
	}
	var b bytes.Buffer
	_ = jpeg.Encode(&b, m, nil)
	img, err := ProcessAvatarImage(b.Bytes())
	if err != nil || img.ContentType != "image/jpeg" {
		t.Fatalf("jpeg: %v %q", err, img.ContentType)
	}
}

func TestProcessAvatarImageRejects(t *testing.T) {
	pngRaw := gradientPNG(t, 8, 8, false)
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)
	gif := []byte("GIF89a\x01\x00\x01\x00\x00\x00\x00;")
	webp := append([]byte("RIFF\x10\x00\x00\x00WEBPVP8 "), make([]byte, 8)...)
	// A PNG signature with a JPEG body: the magic says PNG, the decode fails.
	mismatch := append([]byte("\x89PNG\r\n\x1a\n"), []byte{0xff, 0xd8, 0xff, 0xe0}...)
	// The extension/Content-Type a client claims is never consulted: an
	// HTML file is refused on its bytes.
	html := []byte("<!doctype html><img src=x onerror=alert(1)>")
	big := append(append([]byte{}, pngRaw...), make([]byte, AvatarImageMaxInput)...)
	cases := []struct {
		name string
		raw  []byte
		want error
	}{
		{"svg", svg, ErrAvatarImageType},
		{"gif", gif, ErrAvatarImageType},
		{"html", html, ErrAvatarImageType},
		{"webp", webp, ErrAvatarImageWebP},
		{"magic mismatch", mismatch, ErrAvatarImageCorrupt},
		{"size cap", big, ErrAvatarImageTooLarge},
		{"dimension cap", hugeHeaderPNG(5000, 10), ErrAvatarImageDims},
		{"side cap", hugeHeaderPNG(4096, 4097), ErrAvatarImageDims},
		{"pixel cap", hugeHeaderPNG(4096, 2049), ErrAvatarImageDims},
	}
	for _, tc := range cases {
		if _, err := ProcessAvatarImage(tc.raw); !errors.Is(err, tc.want) {
			t.Errorf("%s: err %v, want %v", tc.name, err, tc.want)
		}
	}
}

// hugeHeaderPNG is a tiny file whose header claims a w×h canvas: a
// decompression bomb must be refused from DecodeConfig, before any pixel.
func hugeHeaderPNG(w, h int) []byte {
	raw := gradientPNG(&testing.T{}, 1, 1, false)
	out := append([]byte{}, raw...)
	binary.BigEndian.PutUint32(out[16:], uint32(w))
	binary.BigEndian.PutUint32(out[20:], uint32(h))
	binary.BigEndian.PutUint32(out[29:], crc32.ChecksumIEEE(out[12:29]))
	return out
}

// Uploads wait for each other (one decode at a time) and all finish; a
// request that gives up while waiting gets its context error.
func TestProcessAvatarImageLimited(t *testing.T) {
	raw := gradientPNG(t, 700, 500, false)
	errs := make(chan error, 4)
	for range 4 {
		go func() {
			_, err := ProcessAvatarImageLimited(context.Background(), raw)
			errs <- err
		}()
	}
	for range 4 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	avatarImageSem <- struct{}{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := ProcessAvatarImageLimited(ctx, raw)
	<-avatarImageSem
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("busy + cancelled: %v", err)
	}
}

// The row-by-row box filter keeps colours: a flat image stays flat.
func TestScaleDownAvatarFlat(t *testing.T) {
	m := image.NewNRGBA(image.Rect(0, 0, 1000, 333))
	for i := 0; i < len(m.Pix); i += 4 {
		m.Pix[i], m.Pix[i+1], m.Pix[i+2], m.Pix[i+3] = 10, 200, 90, 255
	}
	d := scaleDownAvatar(m, 320)
	if d.Bounds().Dx() != 320 || d.Bounds().Dy() != 107 {
		t.Fatalf("size %v", d.Bounds())
	}
	for i := 0; i < len(d.Pix); i += 4 {
		if d.Pix[i] != 10 || d.Pix[i+1] != 200 || d.Pix[i+2] != 90 || d.Pix[i+3] != 255 {
			t.Fatalf("pixel %d = %v", i/4, d.Pix[i:i+4])
		}
	}
}

func TestNormalizeAvatarImageClamps(t *testing.T) {
	a := NormalizeAvatar(Avatar{Kind: AvatarKindImage, Color: "#27b199", Image: &AvatarImage{
		V: "0123456789abcdef", Aspect: 2, Fit: "bogus", X: 999, Y: math.NaN(), Zoom: 99,
	}})
	im := a.Image
	if a.Kind != AvatarKindImage || im == nil || im.Fit != AvatarFitFill || im.X != 150 || im.Y != 0 || im.Zoom != 5 {
		t.Fatalf("not clamped: %+v %+v", a, im)
	}
	if b := NormalizeAvatar(Avatar{Kind: AvatarKindImage, Image: &AvatarImage{V: "../../etc"}}); b.Kind != "" || b.Image != nil {
		t.Fatalf("bad version must fall back to classic: %+v", b)
	}
	if b := NormalizeAvatar(Avatar{Kind: AvatarKindImage}); b.Kind != "" {
		t.Fatalf("image without picture must fall back to classic: %+v", b)
	}
	// Classic/Blob keep the crop (switching back restores the upload); the
	// output-only src never survives.
	if b := NormalizeAvatar(Avatar{Kind: AvatarKindBlob, Image: &AvatarImage{V: "0123456789abcdef", Src: "/x"}}); b.Image == nil || b.Image.Src != "" {
		t.Fatalf("a blob keeps the crop, without src: %+v", b.Image)
	}
	if b := AvatarForCreate(Avatar{Kind: AvatarKindImage, Image: &AvatarImage{V: "0123456789abcdef"}}); b.Kind != "" || b.Image != nil {
		t.Fatalf("a new agent cannot start as an image avatar: %+v", b)
	}
	w := WithImageSrc(a, "/tools/agents", "a 1")
	if w.Image.Src != "/tools/agents/api/team/agents/a%201/avatar/image?v=0123456789abcdef" || a.Image.Src != "" {
		t.Fatalf("src: %q (input changed: %q)", w.Image.Src, a.Image.Src)
	}
}

func TestStoreAvatarImageRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	if err := db.AutoMigrate(&entity.AgentAvatarImage{}); err != nil {
		t.Fatal(err)
	}
	st := NewStore(db)
	p := &entity.AgentPersona{OwnerUserID: "u1", Handle: "pic"}
	if err := st.Create(ctx, p); err != nil {
		t.Fatal(err)
	}
	img, err := ProcessAvatarImage(gradientPNG(t, 40, 40, true))
	if err != nil {
		t.Fatal(err)
	}
	for range 2 { // the second save replaces the row
		if err := st.SaveAvatarImage(ctx, p.ID, img); err != nil {
			t.Fatal(err)
		}
	}
	row, err := st.GetAvatarImage(ctx, p.ID)
	if err != nil || row.Version != img.Version || !bytes.Equal(row.Data, img.Data) {
		t.Fatalf("round trip: %v", err)
	}
	if err := st.Delete(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetAvatarImage(ctx, p.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleting the agent must drop its picture: %v", err)
	}
}
