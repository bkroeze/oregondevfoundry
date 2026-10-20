// Package media imports bounded, normalized product preview images.
package media

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
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
	if format == "jpeg" {
		decoded = orientImage(decoded, jpegOrientation(data))
	}
	width, height := decoded.Bounds().Dx(), decoded.Bounds().Dy()
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

func jpegOrientation(data []byte) int {
	if len(data) < 4 || data[0] != 0xff || data[1] != 0xd8 {
		return 1
	}
	for offset := 2; offset+4 <= len(data); {
		if data[offset] != 0xff {
			return 1
		}
		for offset < len(data) && data[offset] == 0xff {
			offset++
		}
		if offset >= len(data) {
			return 1
		}
		marker := data[offset]
		offset++
		if marker == 0xd9 || marker == 0xda {
			return 1
		}
		if marker == 0x01 || marker >= 0xd0 && marker <= 0xd7 {
			continue
		}
		if offset+2 > len(data) {
			return 1
		}
		length := int(binary.BigEndian.Uint16(data[offset : offset+2]))
		if length < 2 || offset+length > len(data) {
			return 1
		}
		if marker == 0xe1 {
			if orientation := exifOrientation(data[offset+2 : offset+length]); orientation != 1 {
				return orientation
			}
		}
		offset += length
	}
	return 1
}

func exifOrientation(data []byte) int {
	if len(data) < 14 || !bytes.Equal(data[:6], []byte("Exif\x00\x00")) {
		return 1
	}
	tiff := data[6:]
	var order binary.ByteOrder
	switch string(tiff[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 1
	}
	if order.Uint16(tiff[2:4]) != 42 {
		return 1
	}
	ifdOffset := uint64(order.Uint32(tiff[4:8]))
	if ifdOffset+2 > uint64(len(tiff)) {
		return 1
	}
	entryCount := uint64(order.Uint16(tiff[ifdOffset : ifdOffset+2]))
	entriesOffset := ifdOffset + 2
	if entryCount > (uint64(len(tiff))-entriesOffset)/12 {
		return 1
	}
	for index := uint64(0); index < entryCount; index++ {
		entry := tiff[entriesOffset+index*12 : entriesOffset+(index+1)*12]
		if order.Uint16(entry[:2]) == 0x0112 && order.Uint16(entry[2:4]) == 3 && order.Uint32(entry[4:8]) == 1 {
			orientation := int(order.Uint16(entry[8:10]))
			if orientation >= 2 && orientation <= 8 {
				return orientation
			}
			return 1
		}
	}
	return 1
}

func orientImage(source image.Image, orientation int) image.Image {
	if orientation < 2 || orientation > 8 {
		return source
	}
	bounds := source.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	destinationWidth, destinationHeight := width, height
	if orientation >= 5 {
		destinationWidth, destinationHeight = height, width
	}
	destination := image.NewNRGBA(image.Rect(0, 0, destinationWidth, destinationHeight))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			destinationX, destinationY := x, y
			switch orientation {
			case 2:
				destinationX = width - 1 - x
			case 3:
				destinationX, destinationY = width-1-x, height-1-y
			case 4:
				destinationY = height - 1 - y
			case 5:
				destinationX, destinationY = y, x
			case 6:
				destinationX, destinationY = height-1-y, x
			case 7:
				destinationX, destinationY = height-1-y, width-1-x
			case 8:
				destinationX, destinationY = y, width-1-x
			}
			destination.Set(destinationX, destinationY, source.At(bounds.Min.X+x, bounds.Min.Y+y))
		}
	}
	return destination
}
