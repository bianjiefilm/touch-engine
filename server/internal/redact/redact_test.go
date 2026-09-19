package redact

import "testing"

func TestMaskEmail(t *testing.T) {
	cases := map[string]string{
		"merchant@example.com": "m***@example.com",
		"a@b.co":               "a***@b.co",
		"":                     "",
		"noatsign":             "***",
		"@leading":             "***",
	}
	for in, want := range cases {
		if got := MaskEmail(in); got != want {
			t.Fatalf("MaskEmail(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMaskCode(t *testing.T) {
	if got := MaskCode("ABCDEFGH"); got != "AB***GH" {
		t.Fatalf("MaskCode long = %q", got)
	}
	if got := MaskCode("abc"); got != "***" {
		t.Fatalf("MaskCode short = %q", got)
	}
}
