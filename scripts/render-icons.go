//go:build tools

// Render icon outputs from four-times supersampled SVG rasters.
package main

import (
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"

	xdraw "golang.org/x/image/draw"
)

func main() {
	if len(os.Args) != 3 {
		panic("usage: render-icons.go INPUT_DIR OUTPUT_DIR")
	}
	for _, size := range []int{16, 24, 32, 48, 64, 128, 256} {
		input, err := os.Open(filepath.Join(os.Args[1], fmt.Sprintf("%d.png", size)))
		if err != nil {
			panic(err)
		}
		source, err := png.Decode(input)
		_ = input.Close()
		if err != nil {
			panic(err)
		}
		if source.Bounds().Dx() != size*4 || source.Bounds().Dy() != size*4 {
			panic("input must be four-times supersampled")
		}
		out := image.NewNRGBA(image.Rect(0, 0, size, size))
		xdraw.CatmullRom.Scale(out, out.Bounds(), source, source.Bounds(), draw.Src, nil)
		name := fmt.Sprintf("icon-%d.png", size)
		if size == 256 {
			name = "icon.png"
		}
		file, err := os.Create(filepath.Join(os.Args[2], name))
		if err != nil {
			panic(err)
		}
		err = png.Encode(file, out)
		closed := file.Close()
		if err != nil {
			panic(err)
		}
		if closed != nil {
			panic(closed)
		}
	}
}
