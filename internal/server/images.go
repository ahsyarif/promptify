package server

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
)

const maxImageBytes = 5 << 20

var (
	errInvalidImage  = errors.New("unsupported image type")
	errImageTooLarge = errors.New("image too large")
)

var (
	msgInvalidImage  = "Choose a JPEG, PNG, GIF or WebP image."
	msgImageTooLarge = fmt.Sprintf("Image must be %d MB or smaller.", maxImageBytes>>20)
)

var imageExtensions = map[string]string{
	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"image/gif":  ".gif",
	"image/webp": ".webp",
}

// saveImage stores an uploaded image under a random name and returns that
// name. The type is detected from the file content, not the client's claim.
func (s *Server) saveImage(file multipart.File, header *multipart.FileHeader) (string, error) {
	if header.Size > maxImageBytes {
		return "", errImageTooLarge
	}
	head := make([]byte, 512)
	n, err := io.ReadFull(file, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		return "", errInvalidImage
	}
	ext, ok := imageExtensions[http.DetectContentType(head[:n])]
	if !ok {
		return "", errInvalidImage
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", err
	}

	b := make([]byte, 16)
	rand.Read(b)
	name := hex.EncodeToString(b) + ext

	dst, err := os.OpenFile(filepath.Join(s.uploadDir, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(dst, io.LimitReader(file, maxImageBytes+1)); err != nil {
		dst.Close()
		s.removeImage(name)
		return "", err
	}
	if err := dst.Close(); err != nil {
		s.removeImage(name)
		return "", err
	}
	return name, nil
}

func (s *Server) removeImage(name string) {
	if name == "" {
		return
	}
	if err := os.Remove(filepath.Join(s.uploadDir, filepath.Base(name))); err != nil && !errors.Is(err, os.ErrNotExist) {
		s.logger.Warn("remove image", "name", name, "err", err)
	}
}
