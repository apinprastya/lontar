// Command iconrender rasterises an SVG (build/appicon.svg) to PNG files.
//
// Usage:
//
//	iconrender -in appicon.svg SIZE=OUT.png [SIZE=OUT.png ...]
//
// SIZE is either N (an N x N square), WxH, or "svg" to copy the SVG itself
// (e.g. to the hicolor scalable directory). Parent directories are created. The SVG's viewBox is stretched
// to the output size, so non-square outputs need a matching viewBox. Only the
// SVG features supported by github.com/srwiley/oksvg are rendered (paths,
// basic shapes, linear/radial gradients, fill/stroke opacity); anything else
// is reported as an error rather than silently dropped.
package main

import (
	"errors"
	"flag"
	"fmt"
	"image"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/srwiley/oksvg"
	"github.com/srwiley/rasterx"
	"golang.org/x/image/draw"
)

// target is one file to write: a W x H PNG, or a copy of the SVG when
// W and H are zero.
type target struct {
	W, H int
	Path string
}

func main() {
	if err := run(os.Args[1:], os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "iconrender:", err)
		os.Exit(1)
	}
}

func run(args []string, log io.Writer) error {
	fs := flag.NewFlagSet("iconrender", flag.ContinueOnError)
	fs.SetOutput(log)
	in := fs.String("in", "", "input SVG file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *in == "" {
		return errors.New("-in is required")
	}
	if fs.NArg() == 0 {
		return errors.New("no SIZE=OUT.png targets given")
	}
	targets := make([]target, 0, fs.NArg())
	for _, a := range fs.Args() {
		t, err := parseTarget(a)
		if err != nil {
			return err
		}
		targets = append(targets, t)
	}
	for _, t := range targets {
		if t.W == 0 {
			if err := copySVG(*in, t.Path); err != nil {
				return err
			}
			fmt.Fprintf(log, "wrote %s (svg)\n", t.Path)
			continue
		}
		img, err := Render(*in, t.W, t.H)
		if err != nil {
			return err
		}
		if err := writePNG(t.Path, img); err != nil {
			return err
		}
		fmt.Fprintf(log, "wrote %s (%dx%d)\n", t.Path, t.W, t.H)
	}
	return nil
}

// parseTarget parses "N=path" or "WxH=path".
func parseTarget(s string) (target, error) {
	size, path, ok := strings.Cut(s, "=")
	if !ok || path == "" {
		return target{}, fmt.Errorf("target %q: want SIZE=OUT.png", s)
	}
	if size == "svg" {
		return target{Path: path}, nil
	}
	ws, hs, rect := strings.Cut(size, "x")
	if !rect {
		hs = ws
	}
	w, err1 := strconv.Atoi(ws)
	h, err2 := strconv.Atoi(hs)
	if err1 != nil || err2 != nil || w <= 0 || h <= 0 || w > 8192 || h > 8192 {
		return target{}, fmt.Errorf("target %q: invalid size %q", s, size)
	}
	return target{W: w, H: h, Path: path}, nil
}

// minRaster is the smallest size the SVG is rasterised at. oksvg's arc
// flattening misbehaves at small scales (ellipse arcs turn into filled
// bands), so smaller outputs are rendered at this size and downsampled with
// a Catmull-Rom filter, which also gives cleaner small icons.
const minRaster = 1024

// Render rasterises the SVG at path to a w x h image.
func Render(path string, w, h int) (*image.RGBA, error) {
	if w >= minRaster && h >= minRaster {
		return rasterise(path, w, h)
	}
	scale := float64(minRaster) / float64(min(w, h))
	big, err := rasterise(path, int(float64(w)*scale+0.5), int(float64(h)*scale+0.5))
	if err != nil {
		return nil, err
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.CatmullRom.Scale(img, img.Bounds(), big, big.Bounds(), draw.Src, nil)
	return img, nil
}

func rasterise(path string, w, h int) (*image.RGBA, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	// Strict mode: unsupported elements are an error, so the PNG never
	// silently differs from what the SVG shows in a browser.
	icon, err := oksvg.ReadIconStream(f, oksvg.StrictErrorMode)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	icon.SetTarget(0, 0, float64(w), float64(h))
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	scanner := rasterx.NewScannerGV(w, h, img, img.Bounds())
	icon.Draw(rasterx.NewDasher(w, h, scanner), 1)
	return img, nil
}

func copySVG(in, out string) error {
	b, err := os.ReadFile(in)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	return os.WriteFile(out, b, 0o644)
}

func writePNG(path string, img image.Image) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	enc := png.Encoder{CompressionLevel: png.BestCompression}
	if err := enc.Encode(f, img); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
