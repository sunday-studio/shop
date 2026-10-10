package products

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	"image/jpeg"
	_ "image/png" // decoder registration
	"os"
	"path/filepath"

	"golang.org/x/image/draw"
)

const maxImageSide = 1024

var ErrBadImage = errors.New("unsupported or corrupt image; use JPEG or PNG")

// normaliseImage decodes, downsizes to maxImageSide and re-encodes as JPEG.
func normaliseImage(data []byte) ([]byte, error) {
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, ErrBadImage
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w == 0 || h == 0 {
		return nil, ErrBadImage
	}
	if scale := float64(maxImageSide) / float64(max(w, h)); scale < 1 {
		w, h = max(1, int(float64(w)*scale)), max(1, int(float64(h)*scale))
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, b, draw.Src, nil)

	var out bytes.Buffer
	if err := jpeg.Encode(&out, dst, &jpeg.Options{Quality: 85}); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// saveImage stores data content-addressed under dir/products and returns
// the path relative to dir.
func saveImage(dir string, data []byte) (string, error) {
	sum := sha256.Sum256(data)
	name := hex.EncodeToString(sum[:])
	rel := filepath.Join("products", name[:2], name+".jpg")
	abs := filepath.Join(dir, rel)
	if _, err := os.Stat(abs); err == nil {
		return rel, nil
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return "", err
	}
	tmp := abs + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return "", err
	}
	return rel, os.Rename(tmp, abs)
}
