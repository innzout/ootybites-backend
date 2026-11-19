package redact

import "testing"

func TestPhone(t *testing.T) {
	cases := map[string]string{
		"9876543210": "******3210",
		"1234":       "****",
		"":           "****",
		"12345":      "*2345",
	}
	for in, want := range cases {
		if got := Phone(in); got != want {
			t.Errorf("Phone(%q) = %q, want %q", in, got, want)
		}
	}
}
