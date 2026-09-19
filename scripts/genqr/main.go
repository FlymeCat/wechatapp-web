// Command genqr generates QR-code test images into testdata/.
// It uses gozxing's QR writer and renders the bit matrix to a PNG.
package main

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"

	"github.com/makiuchi-d/gozxing"
	"github.com/makiuchi-d/gozxing/qrcode"
)

const scale = 8 // pixels per module

func renderPNG(path, text string) error {
	writer := qrcode.NewQRCodeWriter()
	matrix, err := writer.EncodeWithoutHint(text, gozxing.BarcodeFormat_QR_CODE, 0, 0)
	if err != nil {
		return err
	}
	w, h := matrix.GetWidth(), matrix.GetHeight()
	img := image.NewGray(image.Rect(0, 0, w*scale, h*scale))
	white := color.Gray{Y: 255}
	black := color.Gray{Y: 0}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := white
			if matrix.Get(x, y) {
				c = black
			}
			for dy := 0; dy < scale; dy++ {
				for dx := 0; dx < scale; dx++ {
					img.SetGray(x*scale+dx, y*scale+dy, c)
				}
			}
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		return err
	}
	return nil
}

func main() {
	items := []struct{ path, text string }{
		{"testdata/lottery_qr.png", "05,12,18,23,35,01,08"},                  // 基本票
		{"testdata/lottery_qr_5plus1.png", "05 12 18 23 35 + 01 09"},         // 二等奖 (5+1)
		{"testdata/lottery_qr_3plus1.png", "05,12,18,20,30,01,09"},           // 六等奖 (3+1)
		{"testdata/lottery_qr_noprize.png", "05,12,20,30,32,09,10"},          // 未中奖
		{"testdata/lottery_qr_invalid.png", "hello world"},                   // 无效内容
		{"testdata/lottery_qr_link.png", "https://s.sporttery.cn/HD/wyWLPG"}, // 真实票面二维码（链接）
	}
	for _, it := range items {
		if err := renderPNG(it.path, it.text); err != nil {
			fmt.Fprintf(os.Stderr, "FAIL %s: %v\n", it.path, err)
			os.Exit(1)
		}
		fmt.Println("generated", it.path, "->", it.text)
	}
}
