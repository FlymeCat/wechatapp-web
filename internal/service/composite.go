package service

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"

	xdraw "golang.org/x/image/draw"
)

// BackgroundComposer draws a foreground cutout (with transparency) onto a new
// background. If no background is supplied, the cutout is returned as-is.
type BackgroundComposer struct{}

// ComposeOptions describes how to replace the background.
type ComposeOptions struct {
	// ForegroundPNG is the background-removed cutout returned by remove.bg (transparent PNG).
	ForegroundPNG []byte
	// BackgroundImage is an optional image to place behind the cutout.
	BackgroundImage []byte
	// BackgroundColor is an optional hex color (e.g. "#ffffff" or "#fff").
	// When both BackgroundImage and BackgroundColor are set, the image wins.
	BackgroundColor string
	// ResultFormat is the encoding format of the returned image: "png" (default) or "jpeg".
	ResultFormat string
}

// Compose encodes the final image as bytes.
func (b *BackgroundComposer) Compose(opts ComposeOptions) ([]byte, error) {
	// 1. Decode the cutout (already background-removed).
	fg, err := decodeImage(opts.ForegroundPNG)
	if err != nil {
		return nil, fmt.Errorf("decode foreground: %w", err)
	}
	fgBounds := fg.Bounds()

	// 2. Build the destination canvas. Prefer the background image; fall back to
	//    a solid color; otherwise keep the cutout dimensions (transparent canvas).
	dst := image.NewRGBA(image.Rect(0, 0, fgBounds.Dx(), fgBounds.Dy()))

	switch {
	case opts.BackgroundImage != nil:
		bg, err := decodeImage(opts.BackgroundImage)
		if err != nil {
			return nil, fmt.Errorf("decode background image: %w", err)
		}
		// Scale the background to cover the canvas, then draw it.
		xdraw.CatmullRom.Scale(dst, dst.Bounds(), bg, bg.Bounds(), xdraw.Over, nil)

	case opts.BackgroundColor != "":
		c, err := parseHexColor(opts.BackgroundColor)
		if err != nil {
			return nil, err
		}
		draw.Draw(dst, dst.Bounds(), &image.Uniform{C: c}, image.Point{}, draw.Src)

	default:
		// No background requested: leave the canvas transparent.
	}

	// 3. Draw the cutout on top, centered. image/draw's Over mode does proper
	//    alpha compositing, so transparent pixels reveal the new background.
	offset := image.Pt(
		(dst.Bounds().Dx()-fgBounds.Dx())/2,
		(dst.Bounds().Dy()-fgBounds.Dy())/2,
	)
	draw.Draw(dst, dst.Bounds().Add(offset).Intersect(dst.Bounds()), fg, fgBounds.Min, draw.Over)

	// 4. Encode to the requested format.
	return encodeImage(dst, opts.ResultFormat)
}

func decodeImage(data []byte) (image.Image, error) {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	return img, nil
}

func encodeImage(img image.Image, format string) ([]byte, error) {
	var buf bytes.Buffer
	switch format {
	case "jpeg", "jpg":
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
			return nil, fmt.Errorf("encode jpeg: %w", err)
		}
	default:
		if err := png.Encode(&buf, img); err != nil {
			return nil, fmt.Errorf("encode png: %w", err)
		}
	}
	return buf.Bytes(), nil
}

// parseHexColor converts "#RGB" or "#RRGGBB" (with or without leading '#') to a color.
func parseHexColor(s string) (color.RGBA, error) {
	h := s
	if len(h) > 0 && h[0] == '#' {
		h = h[1:]
	}
	if len(h) == 3 {
		h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
	}
	if len(h) != 6 {
		return color.RGBA{}, fmt.Errorf("invalid background color %q: expected #RGB or #RRGGBB", s)
	}
	var c color.RGBA
	if _, err := fmt.Sscanf(h, "%02x%02x%02x", &c.R, &c.G, &c.B); err != nil {
		return color.RGBA{}, fmt.Errorf("invalid background color %q: %w", s, err)
	}
	c.A = 255
	return c, nil
}
