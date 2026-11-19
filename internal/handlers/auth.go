package handlers

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/innzout/ootybites/internal/middleware"
	"github.com/innzout/ootybites/internal/redact"
	"github.com/innzout/ootybites/internal/services"
	"github.com/innzout/ootybites/internal/validators"
	"github.com/innzout/ootybites/pkg/response"
)

// --- Customer OTP login (build step 3) ---

// OTP codes one phone number may be sent per window, whoever asks. Dev mode
// sends no SMS and shares one machine between every test, so it is relaxed.
const (
	otpPerPhoneMax    = 5
	otpPerPhoneMaxDev = 100
	otpPerPhoneWindow = 15 * time.Minute
)

type otpRequestBody struct {
	Phone string `json:"phone"`
}

// OTPRequest validates the phone and issues an OTP. In dev mode the code is
// returned in the response (dev_otp) for convenience.
func (h *Handlers) OTPRequest(w http.ResponseWriter, r *http.Request) {
	var body otpRequestBody
	if !decodeJSON(w, r, &body) {
		return
	}
	if fields := validators.Fields(map[string]string{
		"phone": validators.PhoneIN(body.Phone),
	}); fields != nil {
		response.FailFields(w, fields)
		return
	}

	// Cap codes per phone number as well as per client. The route limit is keyed
	// by IP, so on its own it lets one person request codes for somebody else's
	// number from many addresses - which, once real SMS is on, is both a way to
	// harass that number and a way to spend the SMS budget. A limiter error
	// fails open, the same as the route limits.
	phoneMax := otpPerPhoneMax
	if h.cfg.OTPDevMode {
		phoneMax = otpPerPhoneMaxDev
	}
	if ok, err := h.limiter.AllowN(r.Context(), "rl:otp_phone:"+body.Phone, phoneMax, otpPerPhoneWindow); err == nil && !ok {
		response.Fail(w, http.StatusTooManyRequests, response.CodeRateLimited,
			"Too many codes requested for this number. Please try again later.")
		return
	}

	devCode, err := h.auth.RequestOTP(r.Context(), body.Phone)
	if err != nil {
		// A misconfigured/unreachable SMS provider is a 503, not a 500: it is a
		// transient dependency failure and the customer should be told to retry
		// rather than shown a generic crash.
		if errors.Is(err, services.ErrSMSNotConfigured) {
			response.Fail(w, http.StatusServiceUnavailable, response.CodeInternal,
				"SMS service is unavailable right now. Please try again shortly.")
			return
		}
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not send code")
		return
	}

	out := map[string]any{"sent": true}
	if devCode != "" {
		out["dev_otp"] = devCode // present only in OTP_DEV_MODE
	}
	response.OK(w, out)
}

type otpVerifyBody struct {
	Phone string `json:"phone"`
	OTP   string `json:"otp"`
}

// OTPVerify checks the code, find-or-creates the customer, and returns a token.
func (h *Handlers) OTPVerify(w http.ResponseWriter, r *http.Request) {
	var body otpVerifyBody
	if !decodeJSON(w, r, &body) {
		return
	}
	if fields := validators.Fields(map[string]string{
		"phone": validators.PhoneIN(body.Phone),
		"otp":   validators.OTP(body.OTP),
	}); fields != nil {
		response.FailFields(w, fields)
		return
	}

	tok, cust, err := h.auth.VerifyOTP(r.Context(), body.Phone, body.OTP)
	if errors.Is(err, services.ErrOTPAttemptsExceeded) {
		slog.Warn("otp locked after repeated wrong codes", "phone", redact.Phone(body.Phone), "ip", middleware.ClientIP(r))
		response.Fail(w, http.StatusUnauthorized, response.CodeUnauthorized,
			"Too many incorrect attempts. Please request a new code.")
		return
	}
	if errors.Is(err, services.ErrInvalidOTP) {
		response.Fail(w, http.StatusUnauthorized, response.CodeUnauthorized, "Invalid or expired code")
		return
	}
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not verify code")
		return
	}
	response.OK(w, map[string]any{"token": tok, "customer": cust})
}

// --- Admin login (build step 3) ---

type adminLoginBody struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// AdminLogin verifies admin credentials and returns an admin-audience token.
func (h *Handlers) AdminLogin(w http.ResponseWriter, r *http.Request) {
	var body adminLoginBody
	if !decodeJSON(w, r, &body) {
		return
	}
	if fields := validators.Fields(map[string]string{
		"username": validators.Required(body.Username),
		"password": validators.Required(body.Password),
	}); fields != nil {
		response.FailFields(w, fields)
		return
	}

	tok, err := h.auth.AdminLogin(r.Context(), body.Username, body.Password)
	if errors.Is(err, services.ErrInvalidCredentials) {
		// Security event: a run of these from one address is a password guess.
		slog.Warn("admin login failed", "username", redact.Username(body.Username), "ip", middleware.ClientIP(r))
		response.Fail(w, http.StatusUnauthorized, response.CodeUnauthorized, "Invalid username or password")
		return
	}
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Login failed")
		return
	}
	response.OK(w, map[string]any{"token": tok})
}
