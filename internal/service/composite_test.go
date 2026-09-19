package service

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

// solidPNG builds a WxH PNG filled with the given color.
func solidPNG(t *testing.T, w, h int, c color.RGBA) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode test png: %v", err)
	}
	return buf.Bytes()
}

func TestParseHexColor(t *testing.T) {
	cases := []struct {
		in   string
		want color.RGBA
		ok   bool
	}{
		{"#ff0000", color.RGBA{255, 0, 0, 255}, true},
		{"#f00", color.RGBA{255, 0, 0, 255}, true},
		{"00ff00", color.RGBA{0, 255, 0, 255}, true},
		{"#0000ff", color.RGBA{0, 0, 255, 255}, true},
		{"red", color.RGBA{}, false},
		{"#12345", color.RGBA{}, false},
	}
	for _, c := range cases {
		got, err := parseHexColor(c.in)
		if (err == nil) != c.ok {
			t.Errorf("parseHexColor(%q) err=%v, want ok=%v", c.in, err, c.ok)
			continue
		}
		if c.ok && got != c.want {
			t.Errorf("parseHexColor(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestComposeWithColor(t *testing.T) {
	// Foreground: 2x2 with a fully transparent center pixel (alpha 0).
	fg := image.NewRGBA(image.Rect(0, 0, 2, 2))
	fg.Set(0, 0, color.RGBA{255, 0, 0, 255})
	fg.Set(1, 0, color.RGBA{0, 255, 0, 255})
	fg.Set(0, 1, color.RGBA{0, 0, 255, 255})
	fg.Set(1, 1, color.RGBA{0, 0, 0, 0}) // transparent -> should show background
	var buf bytes.Buffer
	if err := png.Encode(&buf, fg); err != nil {
		t.Fatal(err)
	}

	c := &BackgroundComposer{}
	out, err := c.Compose(ComposeOptions{
		ForegroundPNG:   buf.Bytes(),
		BackgroundColor: "#00ff00",
	})
	if err != nil {
		t.Fatalf("compose: %v", err)
	}

	img, err := png.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("decode result: %v", err)
	}

	// Transparent center pixel must have been replaced by the green background.
	r, g, b, a := img.At(1, 1).RGBA()
	if r == 0 && g == 0 && b == 0 && a == 0 {
		t.Fatalf("expected transparent pixel to be replaced by background, got transparent")
	}
	if g > r && g > b {
		// green-dominant: OK
	} else {
		t.Errorf("expected green background at (1,1), got r=%d g=%d b=%d", r>>8, g>>8, b>>8)
	}

	// Opaque red pixel must remain red.
	r, g, b, _ = img.At(0, 0).RGBA()
	if r>>8 != 255 || g>>8 != 0 || b>>8 != 0 {
		t.Errorf("expected red foreground at (0,0), got r=%d g=%d b=%d", r>>8, g>>8, b>>8)
	}
}

func TestComposeNoBackgroundReturnsCutout(t *testing.T) {
	data := solidPNG(t, 4, 4, color.RGBA{10, 20, 30, 255})
	c := &BackgroundComposer{}
	out, err := c.Compose(ComposeOptions{ForegroundPNG: data})
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	img, err := png.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if b := img.Bounds(); b.Dx() != 4 || b.Dy() != 4 {
		t.Errorf("expected 4x4 output, got %dx%d", b.Dx(), b.Dy())
	}
}

func TestComposeWithBackgroundImage(t *testing.T) {
	fg := solidPNG(t, 2, 2, color.RGBA{255, 0, 0, 255})
	bg := solidPNG(t, 8, 8, color.RGBA{0, 0, 255, 255})
	c := &BackgroundComposer{}
	out, err := c.Compose(ComposeOptions{
		ForegroundPNG:   fg,
		BackgroundImage: bg,
	})
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	img, err := png.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	// Canvas is cutout-sized (2x2) with the background scaled into it.
	if b := img.Bounds(); b.Dx() != 2 || b.Dy() != 2 {
		t.Errorf("expected 2x2 output, got %dx%d", b.Dx(), b.Dy())
	}
}
