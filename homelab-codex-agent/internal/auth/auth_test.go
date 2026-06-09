package auth

import "testing"

func TestTokenMatches(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		expected string
		actual   string
		want     bool
	}{
		{name: "match", expected: "secret", actual: "secret", want: true},
		{name: "different", expected: "secret", actual: "other", want: false},
		{name: "empty expected", expected: "", actual: "secret", want: false},
		{name: "empty actual", expected: "secret", actual: "", want: false},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := TokenMatches(tc.expected, tc.actual); got != tc.want {
				t.Fatalf("TokenMatches() = %v, want %v", got, tc.want)
			}
		})
	}
}
