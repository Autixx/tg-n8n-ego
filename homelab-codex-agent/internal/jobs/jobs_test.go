package jobs

import "testing"

func TestIsValidJobID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		jobID string
		want  bool
	}{
		{jobID: "20260609-120000-deadbeef", want: true},
		{jobID: "abc.DEF_123-456", want: true},
		{jobID: "../escape", want: false},
		{jobID: "bad/id", want: false},
		{jobID: "bad id", want: false},
		{jobID: ".", want: false},
		{jobID: "..", want: false},
		{jobID: "", want: false},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.jobID, func(t *testing.T) {
			t.Parallel()
			if got := IsValidJobID(tc.jobID); got != tc.want {
				t.Fatalf("IsValidJobID(%q) = %v, want %v", tc.jobID, got, tc.want)
			}
		})
	}
}
