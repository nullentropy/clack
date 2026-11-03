package main

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"testing"
	"time"
)

// patchGIF is a 4x4 GIF: a red frame, then a 2x2 blue patch drawn over it,
// then a green patch after the blue one is disposed back to the red
func patchGIF(t *testing.T) []byte {
	t.Helper()
	pal := color.Palette{color.Transparent, color.RGBA{255, 0, 0, 255}, color.RGBA{0, 0, 255, 255}, color.RGBA{0, 255, 0, 255}}
	fill := func(r image.Rectangle, c uint8) *image.Paletted {
		p := image.NewPaletted(r, pal)
		for i := range p.Pix {
			p.Pix[i] = c
		}
		return p
	}
	g := &gif.GIF{
		Image:    []*image.Paletted{fill(image.Rect(0, 0, 4, 4), 1), fill(image.Rect(0, 0, 2, 2), 2), fill(image.Rect(2, 2, 4, 4), 3)},
		Delay:    []int{5, 0, 20},
		Disposal: []byte{gif.DisposalNone, gif.DisposalPrevious, gif.DisposalNone},
		Config:   image.Config{Width: 4, Height: 4, ColorModel: pal},
	}
	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, g); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestGIFFramesAreWholePicturesOnTheirOwnClock(t *testing.T) {
	a, err := decodeGIF(patchGIF(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(a.frames) != 3 {
		t.Fatalf("%d frames, want 3", len(a.frames))
	}
	at := func(frame, x, y int) color.RGBA {
		img, err := png.Decode(bytes.NewReader(a.frames[frame]))
		if err != nil {
			t.Fatal(err)
		}
		return color.RGBAModel.Convert(img.At(x, y)).(color.RGBA)
	}
	red, blue, green := color.RGBA{255, 0, 0, 255}, color.RGBA{0, 0, 255, 255}, color.RGBA{0, 255, 0, 255}
	if at(1, 0, 0) != blue || at(1, 3, 3) != red {
		t.Fatal("frame 1 should be the blue patch over the red frame")
	}
	if at(2, 0, 0) != red || at(2, 3, 3) != green {
		t.Fatal("frame 2 should drop the blue patch, which asked to be disposed, and add green")
	}
	// browsers play a 0 delay at 100ms, as GIFs from the web expect
	if want := []time.Duration{50 * time.Millisecond, 100 * time.Millisecond, 200 * time.Millisecond}; a.delays[0] != want[0] || a.delays[1] != want[1] || a.delays[2] != want[2] {
		t.Fatalf("delays %v, want %v", a.delays, want)
	}
}

func TestLongGIFsKeepTheirLengthWithFewerFrames(t *testing.T) {
	g := &gif.GIF{Config: image.Config{Width: 300, Height: 300}}
	pal := color.Palette{color.Black, color.White}
	for range 400 {
		g.Image = append(g.Image, image.NewPaletted(image.Rect(0, 0, 300, 300), pal))
		g.Delay = append(g.Delay, 4)
		g.Disposal = append(g.Disposal, gif.DisposalNone)
	}
	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, g); err != nil {
		t.Fatal(err)
	}
	a, err := decodeGIF(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if len(a.frames) == 400 || len(a.frames)*300*300 > gifPixels {
		t.Fatalf("%d frames is over the pixel budget", len(a.frames))
	}
	var total time.Duration
	for _, d := range a.delays {
		total += d
	}
	if total != 16*time.Second {
		t.Fatalf("plays for %v, want the original 16s", total)
	}
}
