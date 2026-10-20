package media

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func encodeTestImage(t *testing.T, format string, width, height int) []byte {
	t.Helper()
	picture := image.NewNRGBA(image.Rect(0, 0, width, height))
	picture.SetNRGBA(0, 0, color.NRGBA{R: 90, G: 120, B: 150, A: 200})
	var encoded bytes.Buffer
	var err error
	switch format {
	case "jpeg":
		err = jpeg.Encode(&encoded, picture, nil)
	case "png":
		err = png.Encode(&encoded, picture)
	case "gif":
		err = gif.Encode(&encoded, picture, nil)
	}
	if err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}

func encodeOrientedJPEG(t *testing.T, orientation int) []byte {
	t.Helper()
	picture := image.NewNRGBA(image.Rect(0, 0, 80, 60))
	colors := []color.NRGBA{
		{R: 240, A: 255},
		{G: 240, A: 255},
		{B: 240, A: 255},
		{R: 240, G: 240, A: 255},
	}
	for y := 0; y < 60; y++ {
		for x := 0; x < 80; x++ {
			picture.SetNRGBA(x, y, colors[(y/30)*2+x/40])
		}
	}
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, picture, &jpeg.Options{Quality: 100}); err != nil {
		t.Fatal(err)
	}
	exif := []byte{
		0xff, 0xe1, 0x00, 0x22,
		'E', 'x', 'i', 'f', 0, 0,
		'I', 'I', 42, 0, 8, 0, 0, 0,
		1, 0,
		0x12, 0x01, 3, 0, 1, 0, 0, 0, byte(orientation), 0, 0, 0,
		0, 0, 0, 0,
	}
	return append(append([]byte{}, encoded.Bytes()[:2]...), append(exif, encoded.Bytes()[2:]...)...)
}

func dominantColorAt(picture image.Image, x, y int) string {
	r, g, b, _ := picture.At(x, y).RGBA()
	switch {
	case r > g*2 && r > b*2:
		return "red"
	case g > r*2 && g > b*2:
		return "green"
	case b > r*2 && b > g*2:
		return "blue"
	case r > b*2 && g > b*2:
		return "yellow"
	default:
		return "unknown"
	}
}

func TestImportNormalizesResizesAndDoesNotUpscale(t *testing.T) {
	for _, test := range []struct {
		name, format          string
		width, height         int
		wantWidth, wantHeight int
	}{
		{"landscape JPEG", "jpeg", 2400, 1600, 1200, 800},
		{"portrait PNG", "png", 800, 2400, 400, 1200},
		{"small transparent PNG", "png", 30, 20, 30, 20},
		{"small JPEG", "jpeg", 20, 30, 20, 30},
		{"thin PNG", "png", 2401, 1, 1200, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), "nested", "media")
			data := encodeTestImage(t, test.format, test.width, test.height)
			result, err := Import(directory, bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			if !ValidFilename(result.Filename) || result.Width != test.wantWidth || result.Height != test.wantHeight {
				t.Fatalf("result = %#v", result)
			}
			file, err := os.Open(filepath.Join(directory, result.Filename))
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			normalized, format, err := image.Decode(file)
			if err != nil || format != test.format || normalized.Bounds().Dx() != test.wantWidth || normalized.Bounds().Dy() != test.wantHeight {
				t.Fatalf("normalized image format=%s, error=%v", format, err)
			}
			if test.name == "small transparent PNG" {
				_, _, _, alpha := normalized.At(0, 0).RGBA()
				if alpha != 200*257 {
					t.Fatalf("PNG alpha changed: %d", alpha)
				}
			}
			duplicate, err := Import(directory, bytes.NewReader(data))
			if err != nil || duplicate != result {
				t.Fatalf("identical import = %#v, %v", duplicate, err)
			}
			files, err := os.ReadDir(directory)
			if err != nil || len(files) != 1 || files[0].Name() != result.Filename {
				t.Fatalf("temporary or duplicate files left behind: %v, %v", files, err)
			}
		})
	}
}

func TestImportAppliesJPEGOrientation(t *testing.T) {
	for _, test := range []struct {
		orientation      int
		width, height    int
		cornerColorNames [4]string
	}{
		{2, 80, 60, [4]string{"green", "red", "yellow", "blue"}},
		{3, 80, 60, [4]string{"yellow", "blue", "green", "red"}},
		{4, 80, 60, [4]string{"blue", "yellow", "red", "green"}},
		{5, 60, 80, [4]string{"red", "blue", "green", "yellow"}},
		{6, 60, 80, [4]string{"blue", "red", "yellow", "green"}},
		{7, 60, 80, [4]string{"yellow", "green", "blue", "red"}},
		{8, 60, 80, [4]string{"green", "yellow", "red", "blue"}},
	} {
		t.Run(string(rune('0'+test.orientation)), func(t *testing.T) {
			directory := t.TempDir()
			result, err := Import(directory, bytes.NewReader(encodeOrientedJPEG(t, test.orientation)))
			if err != nil {
				t.Fatal(err)
			}
			if result.Width != test.width || result.Height != test.height {
				t.Fatalf("result dimensions = %dx%d, want %dx%d", result.Width, result.Height, test.width, test.height)
			}
			file, err := os.Open(filepath.Join(directory, result.Filename))
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			picture, err := jpeg.Decode(file)
			if err != nil {
				t.Fatal(err)
			}
			points := [4]image.Point{{test.width / 4, test.height / 4}, {test.width * 3 / 4, test.height / 4}, {test.width / 4, test.height * 3 / 4}, {test.width * 3 / 4, test.height * 3 / 4}}
			for index, point := range points {
				if got := dominantColorAt(picture, point.X, point.Y); got != test.cornerColorNames[index] {
					t.Fatalf("corner %d color = %s, want %s", index, got, test.cornerColorNames[index])
				}
			}
		})
	}
}

func TestImportStripsMetadataAndPreservesOldImages(t *testing.T) {
	directory := t.TempDir()
	jpegData := encodeTestImage(t, "jpeg", 12, 12)
	comment := []byte("PRIVATE CAMERA METADATA")
	withComment := append([]byte{}, jpegData[:2]...)
	withComment = append(withComment, 0xff, 0xfe, 0, byte(len(comment)+2))
	withComment = append(withComment, comment...)
	withComment = append(withComment, jpegData[2:]...)
	first, err := Import(directory, bytes.NewReader(withComment))
	if err != nil {
		t.Fatal(err)
	}
	firstBytes, err := os.ReadFile(filepath.Join(directory, first.Filename))
	if err != nil || bytes.Contains(firstBytes, comment) {
		t.Fatalf("JPEG metadata not stripped: %v", err)
	}
	pngData := encodeTestImage(t, "png", 14, 14)
	pngData = append(pngData, comment...)
	second, err := Import(directory, bytes.NewReader(pngData))
	if err != nil {
		t.Fatal(err)
	}
	secondBytes, err := os.ReadFile(filepath.Join(directory, second.Filename))
	if err != nil || bytes.Contains(secondBytes, comment) {
		t.Fatalf("PNG trailing payload not stripped: %v", err)
	}
	preserved, err := os.ReadFile(filepath.Join(directory, first.Filename))
	if err != nil || !bytes.Equal(firstBytes, preserved) {
		t.Fatalf("old immutable image changed: %v", err)
	}
}

func TestImportRejectsInvalidOversizedAndUnsupportedImages(t *testing.T) {
	tooManyPixels := encodeTestImage(t, "png", 1, 1)
	// A valid IHDR advertises excessive dimensions without allocating the
	// corresponding bitmap; Import must reject before attempting full decode.
	binary.BigEndian.PutUint32(tooManyPixels[16:20], 5000)
	binary.BigEndian.PutUint32(tooManyPixels[20:24], 4001)
	binary.BigEndian.PutUint32(tooManyPixels[29:33], crc32.ChecksumIEEE(tooManyPixels[12:29]))
	validPNG := encodeTestImage(t, "png", 4, 4)
	cases := map[string]io.Reader{
		"nil":             nil,
		"empty":           strings.NewReader(""),
		"SVG":             strings.NewReader(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`),
		"GIF":             bytes.NewReader(encodeTestImage(t, "gif", 4, 4)),
		"truncated PNG":   bytes.NewReader(validPNG[:len(validPNG)-15]),
		"too many pixels": bytes.NewReader(tooManyPixels),
		"too many bytes":  io.MultiReader(bytes.NewReader(validPNG), bytes.NewReader(make([]byte, MaxBytes))),
	}
	for name, source := range cases {
		t.Run(name, func(t *testing.T) {
			directory := t.TempDir()
			if _, err := Import(directory, source); err == nil {
				t.Fatal("invalid image accepted")
			}
			files, err := os.ReadDir(directory)
			if err != nil || len(files) != 0 {
				t.Fatalf("rejected upload left files: %v, %v", files, err)
			}
		})
	}
}

func TestImportStorageErrorsDoNotExposePaths(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private-storage-path")
	if err := os.WriteFile(path, []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := Import(path, bytes.NewReader(encodeTestImage(t, "png", 3, 3)))
	if err == nil || strings.Contains(err.Error(), path) || strings.Contains(err.Error(), "private-storage-path") {
		t.Fatalf("unsafe storage error = %v", err)
	}
}

func TestValidFilenameRejectsPathsAndUnnormalizedNames(t *testing.T) {
	valid := strings.Repeat("a1", 32) + ".png"
	if !ValidFilename(valid) || !ValidFilename(strings.Repeat("0", 64)+".jpg") {
		t.Fatal("normalized filename rejected")
	}
	for _, name := range []string{"", "../" + valid, "/" + valid, strings.Repeat("A", 64) + ".jpg", strings.Repeat("g", 64) + ".png", valid + ".jpg", "upload.jpg", strings.Repeat("a", 64) + ".jpeg", strings.Repeat("a", 63) + ".png"} {
		if ValidFilename(name) {
			t.Fatalf("unsafe filename accepted: %q", name)
		}
	}
}
