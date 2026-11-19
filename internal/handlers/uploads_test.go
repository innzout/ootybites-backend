package handlers

import "testing"

// The upload type is decided from the file's bytes. These pin that a file is
// accepted for what it is, not for what its name or declared type claims.
func TestSniffImageExt(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")
	jpg := []byte("\xff\xd8\xff\xe0\x00\x10JFIF\x00")
	gif := []byte("GIF89a\x01\x00\x01\x00")
	webp := []byte("RIFF\x24\x00\x00\x00WEBPVP8 ")
	avif := []byte("\x00\x00\x00\x20ftypavif\x00\x00\x00\x00")

	cases := []struct {
		name string
		head []byte
		want string
	}{
		{"png", png, ".png"},
		{"jpeg", jpg, ".jpg"},
		{"gif", gif, ".gif"},
		{"webp", webp, ".webp"},
		{"avif", avif, ".avif"},
		{"html is rejected", []byte("<!doctype html><script>alert(1)</script>"), ""},
		{"svg is rejected (can carry script)", []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`), ""},
		{"php is rejected", []byte("<?php system($_GET['c']); ?>"), ""},
		{"other ISO-BMFF (mp4) is rejected", []byte("\x00\x00\x00\x20ftypisom\x00\x00\x00\x00"), ""},
		{"empty is rejected", []byte{}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sniffImageExt(tc.head); got != tc.want {
				t.Errorf("sniffImageExt = %q, want %q", got, tc.want)
			}
		})
	}
}
