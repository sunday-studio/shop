package products

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/png"
	"testing"
)

// hugePNG builds the header of a PNG that claims w x h pixels without any pixel data. Decoding it
// would allocate w*h*4 bytes; normaliseImage must refuse it from the header alone.
func hugePNG(w, h uint32) []byte {
	var b bytes.Buffer
	b.Write([]byte("\x89PNG\r\n\x1a\n"))
	chunk := func(typ string, data []byte) {
		_ = binary.Write(&b, binary.BigEndian, uint32(len(data)))
		b.WriteString(typ)
		b.Write(data)
		_ = binary.Write(&b, binary.BigEndian, crc32.ChecksumIEEE(append([]byte(typ), data...)))
	}
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], w)
	binary.BigEndian.PutUint32(ihdr[4:], h)
	ihdr[8], ihdr[9] = 8, 6 // 8 bit RGBA
	chunk("IHDR", ihdr)
	chunk("IEND", nil)
	return b.Bytes()
}

func TestNormaliseImageRefusesDecompressionBomb(t *testing.T) {
	for _, d := range [][2]uint32{{60000, 60000}, {20000, 20000}, {1 << 30, 2}} {
		if _, err := normaliseImage(hugePNG(d[0], d[1])); err != ErrBadImage {
			t.Errorf("%dx%d: err = %v, want ErrBadImage", d[0], d[1], err)
		}
	}
}

func TestNormaliseImageStillWorks(t *testing.T) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 2000, 1500))); err != nil {
		t.Fatal(err)
	}
	out, err := normaliseImage(buf.Bytes())
	if err != nil || len(out) == 0 {
		t.Fatalf("normaliseImage: %v", err)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(out))
	if err != nil || cfg.Width != 1024 {
		t.Fatalf("result %dx%d err %v, want width 1024", cfg.Width, cfg.Height, err)
	}
}
