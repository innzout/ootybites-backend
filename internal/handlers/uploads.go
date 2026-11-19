package handlers

import (
	"bytes"
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

	// Decide the type from the bytes, not from what the client says it sent.
	// The Content-Type header and the filename are both chosen by the uploader,
	// so trusting them let any file through under an image name.
	head := make([]byte, 512)
	n, _ := io.ReadFull(file, head)
	head = head[:n]
	ext := sniffImageExt(head)
	if ext == "" {
		response.Fail(w, http.StatusUnprocessableEntity, response.CodeValidation, "Only image files are allowed")
		return
	}
	_ = header // filename and declared type are deliberately not consulted

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
	// Write back the bytes already consumed for sniffing, then the rest.
	if _, err := io.Copy(dst, io.MultiReader(bytes.NewReader(head), file)); err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not store image")
		return
	}

	url := requestScheme(r) + "://" + r.Host + "/uploads/" + name
	response.Created(w, map[string]string{"url": url, "public_id": name})
}

// sniffImageExt returns the extension for an image identified by its leading
// bytes, or "" when the content is not an image we accept.
func sniffImageExt(head []byte) string {
	switch http.DetectContentType(head) {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	}
	// AVIF is an ISO-BMFF file the standard sniffer does not know: bytes 4-11
	// read "ftypavif" (or "ftypavis" for a sequence).
	if len(head) >= 12 && string(head[4:8]) == "ftyp" {
		if brand := string(head[8:12]); brand == "avif" || brand == "avis" {
			return ".avif"
		}
	}
	return ""
}

// uploadsHandler serves stored images and nothing else. http.FileServer alone
// renders an index for a directory request, which listed every uploaded file.
func uploadsHandler() http.Handler {
	files := http.StripPrefix("/uploads/", http.FileServer(http.Dir(uploadsDir)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/uploads/" || strings.HasSuffix(r.URL.Path, "/") {
			http.NotFound(w, r)
			return
		}
		files.ServeHTTP(w, r)
	})
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
