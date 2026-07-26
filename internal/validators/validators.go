// Package validators mirrors the frontend lib/validators.ts so client and
// server share one error language. Each check returns an error string or ""
// (no error); collect them into a fields map and hand to response.FailFields.
package validators

import (
	"regexp"
	"strings"
)

var (
	phoneINRe = regexp.MustCompile(`^[6-9]\d{9}$`)
	pincodeRe = regexp.MustCompile(`^[1-9]\d{5}$`)
	otpRe     = regexp.MustCompile(`^\d{6}$`)
	slugRe    = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
)

// Required fails when the trimmed value is empty.
func Required(v string) string {
	if strings.TrimSpace(v) == "" {
		return "This field is required"
	}
	return ""
}

// PhoneIN validates a 10-digit Indian mobile number (no country code).
func PhoneIN(v string) string {
	if !phoneINRe.MatchString(strings.TrimSpace(v)) {
		return "Enter a valid 10-digit mobile number"
	}
	return ""
}

// Pincode validates a 6-digit Indian PIN code.
func Pincode(v string) string {
	if !pincodeRe.MatchString(strings.TrimSpace(v)) {
		return "Enter a valid 6-digit pincode"
	}
	return ""
}

// OTP validates a 6-digit one-time code.
func OTP(v string) string {
	if !otpRe.MatchString(strings.TrimSpace(v)) {
		return "Enter the 6-digit code"
	}
	return ""
}

// validUnits is the allowed set for a variant's unit (matches the unit_type enum).
var validUnits = map[string]bool{
	"mg": true, "g": true, "kg": true, "ml": true, "l": true, "nos": true, "packets": true,
}

// Unit validates a variant unit against the allowed enum values.
func Unit(v string) string {
	if !validUnits[strings.TrimSpace(v)] {
		return "Choose a valid unit (mg, g, kg, ml, l, nos, packets)"
	}
	return ""
}

// LinkURL validates an optional navigation target: it must be empty, a relative
// path ("/products/..."), or an absolute http(s) URL. This rejects dangerous
// schemes like javascript: and data: that would enable stored XSS when the value
// is later rendered into an href.
func LinkURL(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	if strings.HasPrefix(v, "/") && !strings.HasPrefix(v, "//") {
		return ""
	}
	if strings.HasPrefix(v, "http://") || strings.HasPrefix(v, "https://") {
		return ""
	}
	return "Link must be a relative path (/...) or an http(s) URL"
}

// CouponScope validates a coupon's applicable scope.
func CouponScope(v string) string {
	if v != "all" && v != "specific_products" {
		return "Scope must be 'all' or 'specific_products'"
	}
	return ""
}

// Slug validates a URL slug: lowercase letters, digits and hyphens.
func Slug(v string) string {
	if !slugRe.MatchString(strings.TrimSpace(v)) {
		return "Use lowercase letters, numbers and hyphens only"
	}
	return ""
}

// MinLength fails when the trimmed value is shorter than n.
func MinLength(v string, n int) string {
	if len(strings.TrimSpace(v)) < n {
		return "Too short"
	}
	return ""
}

// PositiveNumber fails when v is not strictly greater than zero.
func PositiveNumber(v float64) string {
	if v <= 0 {
		return "Must be greater than zero"
	}
	return ""
}

// Fields collects non-empty results into an error.fields-shaped map. Pairs are
// (fieldName, errorString); empty error strings are skipped. Returns nil when
// everything passes.
func Fields(pairs map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range pairs {
		if v != "" {
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
