package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/innzout/ootybites/pkg/response"
)

// uploadsDir is where locally-uploaded images are stored and served from.
const uploadsDir = "uploads"

// maxUploadBytes caps a single image upload at 10 MB.
const maxUploadBytes = 10 << 20

// AdminSignImageUpload returns Cloudinary signed-upload params (ARCHITECTURE §8)
// when Cloudinary is configured; otherwise 501 so the UI uses local upload.
func (h *Handlers) AdminSignImageUpload(w http.ResponseWriter, r *http.Request) {
	if !h.cloudinary.Configured() {
		response.Fail(w, http.StatusNotImplemented, response.CodeNotImpl,
			"Cloudinary not configured — use local upload.")
		return
	}
	response.OK(w, h.cloudinary.Sign(r.URL.Query().Get("folder")))
}

// AdminUploadImage accepts a multipart image file, stores it under ./uploads and
// returns an absolute URL. This makes image upload work out-of-the-box without
// any external service (Cloudinary can be layered on later for production).
func (h *Handlers) AdminUploadImage(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes+1024)
	if err := r.ParseMultipartForm(maxUploadBytes); err != nil {
		response.Fail(w, http.StatusRequestEntityTooLarge, response.CodeValidation, "Image too large (max 10 MB)")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		response.Fail(w, http.StatusBadRequest, response.CodeValidation, "No file provided")
		return
	}
	defer file.Close()

	ext := imageExt(header.Header.Get("Content-Type"), header.Filename)
	if ext == "" {
		response.Fail(w, http.StatusUnprocessableEntity, response.CodeValidation, "Only image files are allowed")
		return
	}

	if err := os.MkdirAll(uploadsDir, 0o755); err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not store image")
		return
	}
	name := randomName() + ext
	dst, err := os.Create(filepath.Join(uploadsDir, name))
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not store image")
		return
	}
	defer dst.Close()
	if _, err := io.Copy(dst, file); err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not store image")
		return
	}

	url := requestScheme(r) + "://" + r.Host + "/uploads/" + name
	response.Created(w, map[string]string{"url": url, "public_id": name})
}

// imageExt returns a safe file extension for an image content-type/filename, or
// "" if it isn't a recognised image.
func imageExt(contentType, filename string) string {
	switch strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0])) {
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	case "image/avif":
		return ".avif"
	}
	// Fall back to the filename extension if it's a known image type.
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".jpg", ".jpeg":
		return ".jpg"
	case ".png":
		return ".png"
	case ".webp":
		return ".webp"
	case ".gif":
		return ".gif"
	case ".avif":
		return ".avif"
	}
	return ""
}

func randomName() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func requestScheme(r *http.Request) string {
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		return "https"
	}
	return "http"
}
