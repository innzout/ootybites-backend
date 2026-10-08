package services

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/innzout/ootybites/internal/config"
)

// ErrSMSNotConfigured is returned when live OTP delivery is requested but the
// MSG91 credentials are absent. Surfaced as a 503 rather than a silent success,
// because "OTP sent" with no SMS is the worst possible outcome for a customer.
var ErrSMSNotConfigured = errors.New("sms provider not configured")

// SMSSender delivers a one-time code to a phone number.
type SMSSender interface {
	SendOTP(ctx context.Context, phone, code string) error
}

// MSG91 sends OTPs through MSG91's transactional flow API.
type MSG91 struct {
	authKey    string
	templateID string
	senderID   string
	http       *http.Client
}

// NewMSG91 builds the sender from config. Returns nil when credentials are
// missing so the caller can decide how to behave (dev mode tolerates it).
func NewMSG91(cfg *config.Config) *MSG91 {
	if cfg.MSG91AuthKey == "" || cfg.MSG91TemplateID == "" {
		return nil
	}
	return &MSG91{
		authKey:    cfg.MSG91AuthKey,
		templateID: cfg.MSG91TemplateID,
		senderID:   cfg.MSG91SenderID,
		// A hung SMS provider must not hold an API worker open; the customer
		// gets a fast failure and can retry instead.
		http: &http.Client{Timeout: 8 * time.Second},
	}
}

const msg91FlowURL = "https://control.msg91.com/api/v5/flow/"

// msg91Recipient is one entry in the flow payload. "mobiles" must be the number
// in international format without a "+", and the template's variable name (##OTP##
// in the MSG91 template editor) maps to the "otp" key.
type msg91Recipient struct {
	Mobiles string `json:"mobiles"`
	OTP     string `json:"otp"`
}

type msg91Request struct {
	TemplateID string           `json:"template_id"`
	SenderID   string           `json:"sender,omitempty"`
	ShortURL   string           `json:"short_url"`
	Recipients []msg91Recipient `json:"recipients"`
}

// SendOTP delivers the code. MSG91 answers 200 with {"type":"error"} on some
// failures, so the body is checked as well as the status code.
func (m *MSG91) SendOTP(ctx context.Context, phone, code string) error {
	body, err := json.Marshal(msg91Request{
		TemplateID: m.templateID,
		SenderID:   m.senderID,
		ShortURL:   "0",
		Recipients: []msg91Recipient{{Mobiles: normaliseMobile(phone), OTP: code}},
	})
	if err != nil {
		return fmt.Errorf("msg91 encode: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, msg91FlowURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("msg91 request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("accept", "application/json")
	req.Header.Set("authkey", m.authKey)

	res, err := m.http.Do(req)
	if err != nil {
		return fmt.Errorf("msg91 send: %w", err)
	}
	defer res.Body.Close()

	// Cap the read — a provider must not be able to exhaust our memory.
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 4<<10))
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("msg91 send: status %d: %s", res.StatusCode, strings.TrimSpace(string(raw)))
	}

	var out struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &out); err == nil && strings.EqualFold(out.Type, "error") {
		return fmt.Errorf("msg91 send: %s", out.Message)
	}
	return nil
}

// normaliseMobile puts a number into the digits-only international form MSG91
// expects, defaulting to the India country code for the 10-digit numbers the
// storefront validates.
func normaliseMobile(phone string) string {
	var digits strings.Builder
	for _, r := range phone {
		if r >= '0' && r <= '9' {
			digits.WriteRune(r)
		}
	}
	d := digits.String()
	if len(d) == 10 {
		return "91" + d
	}
	return d
}
