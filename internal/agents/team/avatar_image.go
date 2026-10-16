package team

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	"image/draw"
	"image/jpeg"
	"image/png"
	"math"
	"slices"
	"time"

	"github.com/yogasw/wick/internal/entity"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Limits of an uploaded avatar picture. The input cap bounds the request,
// the side cap bounds what a decode may allocate (checked on the header,
// before the pixels are read, so a small file claiming a huge canvas is
// refused unread), and the output is scaled down to AvatarImageOutSide
// and must fit AvatarImageMaxOut once re-encoded.
const (
	AvatarImageMaxInput = 2 << 20   // 2 MiB as uploaded
	AvatarImageMaxSide  = 4096      // px, either side, read from the header
	AvatarImageMaxPix   = 8 << 20   // px in total (4096×2048)
	AvatarImageOutSide  = 320       // px, longest side once re-encoded
	AvatarImageMaxOut   = 256 << 10 // 256 KiB once re-encoded
)

// Crop bounds, the same as the editor's (mockup avatar-settings-image).
const (
	AvatarCropMaxOffset = 150.0 // percent of the frame, either way
	AvatarZoomMin       = 0.3
	AvatarZoomMax       = 5.0
)

// Avatar image fit modes: what zoom 0 (automatic) means.
const (
	AvatarFitFill = "fill"
	AvatarFitFit  = "fit"
)

// AvatarCrops are the frame shapes an image avatar is cut to, the same keys
// as AVATAR_CROPS in fe/common/avatar/src/image.ts. "none" is the plain
// square picture; anything unknown (or absent, as on avatars saved before
// crops existed) is the round frame.
var AvatarCrops = []string{"round", "rounded", "hex", "none"}

// AvatarCropRound is the default crop.
const AvatarCropRound = "round"

// Errors of ProcessAvatarImage, each one a 4xx for the caller.
var (
	ErrAvatarImageTooLarge  = errors.New("image is larger than 2 MB")
	ErrAvatarImageType      = errors.New("only PNG, JPEG or WebP images are accepted")
	ErrAvatarImageWebP      = errors.New("WebP cannot be decoded on this server; upload a PNG or JPEG")
	ErrAvatarImageDims      = errors.New("image is larger than 4096 pixels a side or 8 megapixels")
	ErrAvatarImageCorrupt   = errors.New("image could not be read")
	ErrAvatarImageOutTooBig = errors.New("image is still too large once scaled down")
)

// Image formats told apart by their first bytes, never by the name or the
// Content-Type the client sent.
const (
	avatarFmtPNG  = "png"
	avatarFmtJPEG = "jpeg"
	avatarFmtWebP = "webp"
)

// sniffAvatarImage returns the format of raw by its magic bytes, "" for
// anything else (SVG, GIF, HTML, a polyglot's text head, …).
func sniffAvatarImage(raw []byte) string {
	switch {
	case bytes.HasPrefix(raw, []byte("\x89PNG\r\n\x1a\n")):
		return avatarFmtPNG
	case bytes.HasPrefix(raw, []byte{0xff, 0xd8, 0xff}):
		return avatarFmtJPEG
	case len(raw) >= 12 && bytes.Equal(raw[:4], []byte("RIFF")) && bytes.Equal(raw[8:12], []byte("WEBP")):
		return avatarFmtWebP
	}
	return ""
}

// ProcessedAvatarImage is an upload after ProcessAvatarImage: new bytes the
// server encoded itself, so no metadata (EXIF, text chunks) and nothing
// appended to the original file survives.
type ProcessedAvatarImage struct {
	Data          []byte
	ContentType   string
	Version       string
	Width, Height int
}

// avatarImageSem lets one upload decode at a time: at the 8 Mpx cap a
// decode can take about 64 MiB (16-bit PNG, 8 bytes a pixel) and, by the
// stdlib's layout, roughly 120 MiB for a progressive 4:4:4 JPEG
// (coefficients + YCbCr; estimated, not measured), and the host is small.
// Others wait their turn, or give up with the request.
var avatarImageSem = make(chan struct{}, 1)

// ProcessAvatarImageLimited is ProcessAvatarImage behind avatarImageSem.
func ProcessAvatarImageLimited(ctx context.Context, raw []byte) (ProcessedAvatarImage, error) {
	select {
	case avatarImageSem <- struct{}{}:
	case <-ctx.Done():
		return ProcessedAvatarImage{}, ctx.Err()
	}
	defer func() { <-avatarImageSem }()
	return ProcessAvatarImage(raw)
}

// ProcessAvatarImage checks, decodes, scales down and re-encodes one
// uploaded picture. PNG and JPEG only; WebP is recognised but this build
// has no WebP decoder (golang.org/x/image is not a dependency), so it is
// refused with its own message. An opaque picture is stored as JPEG, one
// with transparency as PNG.
func ProcessAvatarImage(raw []byte) (ProcessedAvatarImage, error) {
	var out ProcessedAvatarImage
	if len(raw) > AvatarImageMaxInput {
		return out, ErrAvatarImageTooLarge
	}
	var (
		cfg image.Config
		err error
	)
	format := sniffAvatarImage(raw)
	switch format {
	case avatarFmtPNG:
		cfg, err = png.DecodeConfig(bytes.NewReader(raw))
	case avatarFmtJPEG:
		cfg, err = jpeg.DecodeConfig(bytes.NewReader(raw))
	case avatarFmtWebP:
		return out, ErrAvatarImageWebP
	default:
		return out, ErrAvatarImageType
	}
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 {
		return out, ErrAvatarImageCorrupt
	}
	if cfg.Width > AvatarImageMaxSide || cfg.Height > AvatarImageMaxSide || cfg.Width*cfg.Height > AvatarImageMaxPix {
		return out, ErrAvatarImageDims
	}
	var src image.Image
	if format == avatarFmtPNG {
		src, err = png.Decode(bytes.NewReader(raw))
	} else {
		src, err = jpeg.Decode(bytes.NewReader(raw))
	}
	if err != nil {
		return out, ErrAvatarImageCorrupt
	}
	dst := scaleDownAvatar(src, AvatarImageOutSide)
	var buf bytes.Buffer
	if opaqueRGBA(dst) {
		err = jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 85})
		out.ContentType = "image/jpeg"
	} else {
		err = (&png.Encoder{CompressionLevel: png.BestCompression}).Encode(&buf, dst)
		out.ContentType = "image/png"
	}
	if err != nil {
		return out, ErrAvatarImageCorrupt
	}
	if buf.Len() > AvatarImageMaxOut {
		return out, ErrAvatarImageOutTooBig
	}
	sum := sha256.Sum256(buf.Bytes())
	out.Data = buf.Bytes()
	out.Version = hex.EncodeToString(sum[:8])
	out.Width, out.Height = dst.Bounds().Dx(), dst.Bounds().Dy()
	return out, nil
}

// scaleDownAvatar draws src into a fresh RGBA no wider or taller than
// side, averaging each box of source pixels (stdlib only; no x/image).
// It reads src one row at a time, so the only full-size buffer is the
// decoded image itself.
func scaleDownAvatar(src image.Image, side int) *image.RGBA {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	dw, dh := w, h
	if w > side || h > side {
		r := float64(side) / float64(max(w, h))
		dw, dh = max(1, int(math.Round(float64(w)*r))), max(1, int(math.Round(float64(h)*r)))
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	row := image.NewRGBA(image.Rect(0, 0, w, 1))
	acc := make([]int, dw*4)
	cnt := make([]int, dw)
	for sy := 0; sy < h; sy++ {
		draw.Draw(row, row.Bounds(), src, image.Pt(b.Min.X, b.Min.Y+sy), draw.Src)
		for sx := 0; sx < w; sx++ {
			dx, i := sx*dw/w, sx*4
			acc[dx*4] += int(row.Pix[i])
			acc[dx*4+1] += int(row.Pix[i+1])
			acc[dx*4+2] += int(row.Pix[i+2])
			acc[dx*4+3] += int(row.Pix[i+3])
			cnt[dx]++
		}
		dy := sy * dh / h
		if sy+1 < h && (sy+1)*dh/h == dy {
			continue
		}
		o := dst.PixOffset(0, dy)
		for dx := 0; dx < dw; dx++ {
			n := max(1, cnt[dx])
			for k := 0; k < 4; k++ {
				dst.Pix[o+dx*4+k] = uint8(acc[dx*4+k] / n)
				acc[dx*4+k] = 0
			}
			cnt[dx] = 0
		}
	}
	return dst
}

func opaqueRGBA(m *image.RGBA) bool {
	for i := 3; i < len(m.Pix); i += 4 {
		if m.Pix[i] != 0xff {
			return false
		}
	}
	return true
}

// clampAvatarImage keeps the crop numbers inside the editor's bounds; a
// NaN or infinite value reads as the default.
func clampAvatarImage(im AvatarImage) AvatarImage {
	if im.Fit != AvatarFitFit {
		im.Fit = AvatarFitFill
	}
	if !slices.Contains(AvatarCrops, im.Crop) {
		im.Crop = AvatarCropRound
	}
	im.X = clampFloat(im.X, -AvatarCropMaxOffset, AvatarCropMaxOffset, 0)
	im.Y = clampFloat(im.Y, -AvatarCropMaxOffset, AvatarCropMaxOffset, 0)
	if im.Zoom != 0 {
		im.Zoom = clampFloat(im.Zoom, AvatarZoomMin, AvatarZoomMax, 0)
	}
	im.Aspect = clampFloat(im.Aspect, 1.0/AvatarImageMaxSide, AvatarImageMaxSide, 1)
	return im
}

func clampFloat(v, lo, hi, def float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return def
	}
	return math.Min(hi, math.Max(lo, v))
}

// SaveAvatarImage stores (or replaces) agent id's picture.
func (s *Store) SaveAvatarImage(ctx context.Context, id string, p ProcessedAvatarImage) error {
	row := entity.AgentAvatarImage{
		AgentID: id, ContentType: p.ContentType, Version: p.Version,
		Width: p.Width, Height: p.Height, Data: p.Data, UpdatedAt: time.Now().UTC(),
	}
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "agent_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"content_type", "version", "width", "height", "data", "updated_at"}),
	}).Create(&row).Error
}

// GetAvatarImage returns agent id's picture, ErrNotFound when it has none.
func (s *Store) GetAvatarImage(ctx context.Context, id string) (entity.AgentAvatarImage, error) {
	var row entity.AgentAvatarImage
	err := s.db.WithContext(ctx).Where("agent_id = ?", id).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return row, ErrNotFound
	}
	return row, err
}

// DeleteAvatarImage drops agent id's picture; none is not an error.
func (s *Store) DeleteAvatarImage(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).Where("agent_id = ?", id).Delete(&entity.AgentAvatarImage{}).Error
}
