package files

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	_ "image/gif"
	_ "image/png"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

var ErrNoThumb = errors.New("no thumbnail for this file")

// Thumbnailer renders and caches small JPEG previews of images.
type Thumbnailer struct {
	cache string
	slots chan struct{}
}

func NewThumbnailer(cacheDir string) (*Thumbnailer, error) {
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		return nil, err
	}
	return &Thumbnailer{cache: cacheDir, slots: make(chan struct{}, 3)}, nil
}

const (
	thumbSize      = 400
	maxThumbSource = 80 << 20 // bytes
	// Decoded pixels, a guard against decompression bombs: an 8-bit image
	// takes up to 4 bytes a pixel once decoded, a 16-bit one 8.
	maxThumbPixels = 50_000_000
	maxDeepPixels  = 24_000_000
)

// decodable reports whether an image of this size is small enough to decode.
func decodable(cfg image.Config) bool {
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return false
	}
	px := int64(cfg.Width) * int64(cfg.Height)
	switch cfg.ColorModel {
	case color.RGBA64Model, color.NRGBA64Model, color.Gray16Model:
		return px <= maxDeepPixels
	}
	return px <= maxThumbPixels
}

func Thumbable(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp":
		return true
	}
	return false
}

// Get returns the path of a cached thumbnail for p on drive d (named driveID).
func (t *Thumbnailer) Get(ctx context.Context, driveID string, d Drive, p string) (string, error) {
	if !Thumbable(p) {
		return "", ErrNoThumb
	}
	info, err := d.Stat(ctx, p)
	if err != nil {
		return "", err
	}
	if info.IsDir || info.Size > maxThumbSource {
		return "", ErrNoThumb
	}
	sum := sha256.Sum256(fmt.Appendf(nil, "%s\x00%s\x00%d\x00%d", driveID, p, info.Size, info.ModTime))
	key := hex.EncodeToString(sum[:])
	out := filepath.Join(t.cache, key[:2], key+".jpg")
	if _, err := os.Stat(out); err == nil {
		return out, nil
	}

	t.slots <- struct{}{}
	defer func() { <-t.slots }()

	rc, _, err := d.Open(ctx, p)
	if err != nil {
		return "", err
	}
	defer rc.Close()
	// Buffer remote objects once instead of seeking over the network.
	var src io.ReadSeeker = rc
	if _, ok := rc.(*os.File); !ok {
		b, err := io.ReadAll(io.LimitReader(rc, maxThumbSource+1))
		if err != nil {
			return "", err
		}
		src = bytes.NewReader(b)
	}
	return out, t.render(src, out)
}

// FromFile returns a cached thumbnail of a local image file (a generated
// still), keyed by key.
func (t *Thumbnailer) FromFile(key, file string) (string, error) {
	sum := sha256.Sum256([]byte("still\x00" + key))
	k := hex.EncodeToString(sum[:])
	out := filepath.Join(t.cache, k[:2], k+".jpg")
	if _, err := os.Stat(out); err == nil {
		return out, nil
	}
	f, err := os.Open(file)
	if err != nil {
		return "", err
	}
	defer f.Close()
	t.slots <- struct{}{}
	defer func() { <-t.slots }()
	return out, t.render(f, out)
}

// Watermarked returns the cached watermarked preview for key, rendering it
// with render the first time. Watermarks carry the date, so these are only
// kept for a day or two (see PruneWatermarked).
func (t *Thumbnailer) Watermarked(key string, render func() ([]byte, error)) (string, error) {
	sum := sha256.Sum256([]byte("wm\x00" + key))
	k := hex.EncodeToString(sum[:])
	out := filepath.Join(t.cache, "wm", k[:2], k+".jpg")
	if _, err := os.Stat(out); err == nil {
		return out, nil
	}
	b, err := render()
	if err != nil {
		return "", err
	}
	return out, writeAtomic(out, b)
}

// PruneWatermarked removes watermarked previews made more than age ago.
func (t *Thumbnailer) PruneWatermarked(age time.Duration) {
	cutoff := time.Now().Add(-age)
	root := filepath.Join(t.cache, "wm")
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, err := d.Info(); err == nil && info.ModTime().Before(cutoff) {
			_ = os.Remove(p)
		}
		return nil
	})
}

func writeAtomic(out string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(out), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(out), "wm-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), out)
}

func (t *Thumbnailer) render(src io.ReadSeeker, out string) error {
	cfg, _, err := image.DecodeConfig(src)
	if err != nil || !decodable(cfg) {
		return ErrNoThumb
	}
	if _, err := src.Seek(0, 0); err != nil {
		return err
	}
	img, _, err := image.Decode(src)
	if err != nil {
		return ErrNoThumb
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w > thumbSize || h > thumbSize {
		if w >= h {
			h, w = max(1, h*thumbSize/w), thumbSize
		} else {
			w, h = max(1, w*thumbSize/h), thumbSize
		}
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(dst, dst.Bounds(), image.White, image.Point{}, draw.Src)
	draw.ApproxBiLinear.Scale(dst, dst.Bounds(), img, b, draw.Over, nil)

	if err := os.MkdirAll(filepath.Dir(out), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(out), "thumb-*")
	if err != nil {
		return err
	}
	if err := jpeg.Encode(tmp, dst, &jpeg.Options{Quality: 82}); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	tmp.Close()
	return os.Rename(tmp.Name(), out)
}
