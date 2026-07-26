package services

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/innzout/ootybites/internal/config"
)

// Cloudinary produces signed-upload parameters so the browser can upload files
// directly to Cloudinary without the API secret ever leaving the server
// (ARCHITECTURE §8). The secret is used only to compute the signature here.
type Cloudinary struct {
	cloudName string
	apiKey    string
	apiSecret string
}

// NewCloudinary builds the service from config.
func NewCloudinary(cfg *config.Config) *Cloudinary {
	return &Cloudinary{
		cloudName: cfg.CloudinaryCloudName,
		apiKey:    cfg.CloudinaryAPIKey,
		apiSecret: cfg.CloudinaryAPISecret,
	}
}

// Configured reports whether all Cloudinary credentials are present.
func (c *Cloudinary) Configured() bool {
	return c.cloudName != "" && c.apiKey != "" && c.apiSecret != ""
}

// SignResult is everything the browser needs to perform a signed upload.
type SignResult struct {
	CloudName string `json:"cloud_name"`
	APIKey    string `json:"api_key"`
	Timestamp int64  `json:"timestamp"`
	Folder    string `json:"folder"`
	Signature string `json:"signature"`
}

// Sign computes a Cloudinary upload signature for the given folder. Cloudinary
// signs the alphabetically-sorted upload params (here: folder, timestamp)
// joined as k=v&k=v, with the API secret appended, hashed with SHA-1.
func (c *Cloudinary) Sign(folder string) SignResult {
	if folder == "" {
		folder = "ootybites"
	}
	ts := time.Now().Unix()
	toSign := fmt.Sprintf("folder=%s&timestamp=%d%s", folder, ts, c.apiSecret)
	sum := sha1.Sum([]byte(toSign))
	return SignResult{
		CloudName: c.cloudName,
		APIKey:    c.apiKey,
		Timestamp: ts,
		Folder:    folder,
		Signature: hex.EncodeToString(sum[:]),
	}
}
