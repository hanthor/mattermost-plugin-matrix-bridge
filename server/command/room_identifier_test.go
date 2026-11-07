package command

import "testing"

func TestIsValidRoomIdentifier(t *testing.T) {
	cases := map[string]bool{
		"#room:example.com":        true,
		"!abc123:example.com":      true,
		"!I3qYeqhS0kucQqNHITx9d4F": true, // room version 12: no server part
		"#room":                    false,
		"!":                        false,
		"room:example.com":         false,
		"!with space:example.com":  false,
		"":                         false,
	}
	for in, want := range cases {
		if got := isValidRoomIdentifier(in); got != want {
			t.Errorf("isValidRoomIdentifier(%q) = %v, want %v", in, got, want)
		}
	}
}
