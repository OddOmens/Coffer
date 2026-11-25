package files

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"os"
	"sync"

	"golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

const watermarkMax = 2000 // longest side of a watermarked preview

var (
	wmFontOnce sync.Once
	wmFont     *opentype.Font
)

// WatermarkJPEG decodes an image from r, scales it down to a preview size,
// stamps text across it and returns a JPEG. Watermarked previews are
// deliberately smaller than the original.
func WatermarkJPEG(ctx context.Context, d Drive, p string, text string) ([]byte, error) {
	rc, info, err := d.Open(ctx, p)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	if info.Size > maxThumbSource {
		return nil, ErrNoThumb
	}
	return watermarkFrom(rc, text)
}

// WatermarkFile is WatermarkJPEG for a local file, such as a generated still.
func WatermarkFile(file, text string) ([]byte, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return watermarkFrom(f, text)
}

func watermarkFrom(r io.Reader, text string) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, maxThumbSource+1))
	if err != nil {
		return nil, err
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil || !decodable(cfg) {
		return nil, ErrNoThumb
	}
	img, _, err := image.Decode(bytes.NewReader(b))
	if err != nil {
		return nil, ErrNoThumb
	}
	sb := img.Bounds()
	w, h := sb.Dx(), sb.Dy()
	if w > watermarkMax || h > watermarkMax {
		if w >= h {
			h, w = max(1, h*watermarkMax/w), watermarkMax
		} else {
			w, h = max(1, w*watermarkMax/h), watermarkMax
		}
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(dst, dst.Bounds(), image.White, image.Point{}, draw.Src)
	draw.ApproxBiLinear.Scale(dst, dst.Bounds(), img, sb, draw.Over, nil)
	stamp(dst, text)
	var out bytes.Buffer
	if err := jpeg.Encode(&out, dst, &jpeg.Options{Quality: 85}); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// stamp tiles text over img in staggered rows, light with a dark outline so
// it shows on any background.
func stamp(img *image.RGBA, text string) {
	wmFontOnce.Do(func() { wmFont, _ = opentype.Parse(goregular.TTF) })
	if wmFont == nil || text == "" {
		return
	}
	b := img.Bounds()
	size := float64(max(14, min(b.Dx(), b.Dy())/28))
	face, err := opentype.NewFace(wmFont, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingNone})
	if err != nil {
		return
	}
	defer face.Close()
	adv := font.MeasureString(face, text).Ceil()
	gapX, gapY := adv+int(size*3), int(size*5)
	shadow := image.NewUniform(color.NRGBA{0, 0, 0, 70})
	light := image.NewUniform(color.NRGBA{255, 255, 255, 110})
	for row, y := 0, int(size*2); y < b.Dy()+gapY; row, y = row+1, y+gapY {
		offset := (row % 2) * gapX / 2
		for x := -offset; x < b.Dx(); x += gapX {
			for _, o := range [][2]int{{1, 1}, {-1, -1}, {1, -1}, {-1, 1}} {
				dr := &font.Drawer{Dst: img, Src: shadow, Face: face, Dot: fixed.P(x+o[0], y+o[1])}
				dr.DrawString(text)
			}
			dr := &font.Drawer{Dst: img, Src: light, Face: face, Dot: fixed.P(x, y)}
			dr.DrawString(text)
		}
	}
}
