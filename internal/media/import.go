// Package media imports bounded, normalized product preview images.
package media

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/image/draw"
)

const (
	MaxBytes  = 8 << 20
	MaxPixels = 20_000_000
	MaxEdge   = 1200
)

type Result struct {
	Filename string
	Width    int
	Height   int
}

// ValidFilename accepts only names produced by Import, never paths or original
// upload names. It is shared by persistence and public media serving.
func ValidFilename(filename string) bool {
	if len(filename) != 68 || (!strings.HasSuffix(filename, ".jpg") && !strings.HasSuffix(filename, ".png")) {
		return false
	}
	for _, c := range filename[:64] {
		if !(c >= '0' && c <= '9') && !(c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// Import accepts JPEG/PNG only, strips metadata, and atomically publishes an
// immutable content-addressed image. Errors are safe to display to uploaders.
func Import(directory string, source io.Reader) (Result, error) {
	if source == nil {
		return Result{}, errors.New("Choose a JPEG or PNG image.")
	}
	data, err := io.ReadAll(io.LimitReader(source, MaxBytes+1))
	if err != nil {
		return Result{}, errors.New("The uploaded image could not be read.")
	}
	if len(data) > MaxBytes {
		return Result{}, errors.New("Images must be no larger than 8 MiB.")
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || (format != "jpeg" && format != "png") {
		return Result{}, errors.New("Upload a valid JPEG or PNG image.")
	}
	if config.Width <= 0 || config.Height <= 0 || config.Width > MaxPixels/config.Height {
		return Result{}, errors.New("Images must contain no more than 20 million pixels.")
	}
	decoded, decodedFormat, err := image.Decode(bytes.NewReader(data))
	if err != nil || decodedFormat != format {
		return Result{}, errors.New("The JPEG or PNG image could not be decoded.")
	}
	width, height := config.Width, config.Height
	if width > MaxEdge || height > MaxEdge {
		if width >= height {
			height = max(1, height*MaxEdge/width)
			width = MaxEdge
		} else {
			width = max(1, width*MaxEdge/height)
			height = MaxEdge
		}
		resized := image.NewNRGBA(image.Rect(0, 0, width, height))
		draw.ApproxBiLinear.Scale(resized, resized.Bounds(), decoded, decoded.Bounds(), draw.Src, nil)
		decoded = resized
	}
	if err := os.MkdirAll(directory, 0755); err != nil {
		return Result{}, errors.New("The image could not be saved. Please try again later.")
	}
	file, err := os.CreateTemp(directory, ".product-image-*")
	if err != nil {
		return Result{}, errors.New("The image could not be saved. Please try again later.")
	}
	defer os.Remove(file.Name())
	defer file.Close()
	hash := sha256.New()
	writer := io.MultiWriter(file, hash)
	extension := ".png"
	if format == "jpeg" {
		extension = ".jpg"
		err = jpeg.Encode(writer, decoded, &jpeg.Options{Quality: 85})
	} else {
		err = png.Encode(writer, decoded)
	}
	if err != nil {
		return Result{}, errors.New("The image could not be saved. Please try again later.")
	}
	if err := file.Chmod(0644); err != nil {
		return Result{}, errors.New("The image could not be saved. Please try again later.")
	}
	if err := file.Close(); err != nil {
		return Result{}, errors.New("The image could not be saved. Please try again later.")
	}
	filename := hex.EncodeToString(hash.Sum(nil)) + extension
	// Linking a fully encoded temporary file publishes atomically without
	// overwriting existing content. Identical normalized imports reuse a file.
	if err := os.Link(file.Name(), filepath.Join(directory, filename)); err != nil && !errors.Is(err, os.ErrExist) {
		return Result{}, errors.New("The image could not be saved. Please try again later.")
	}
	return Result{Filename: filename, Width: width, Height: height}, nil
}
