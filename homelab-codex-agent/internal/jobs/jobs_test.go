package jobs

import (
	"encoding/json"
	"testing"
)

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

func TestRequestParsesDashboardAttachments(t *testing.T) {
	t.Parallel()
	data := []byte(`{
		"mode":"structured_breakdown",
		"text":"Analyze",
		"fileName":"ui.png",
		"attachments":[{
			"id":"ATT_1","kind":"image","fileName":"ui.png",
			"mimeType":"image/png","sizeBytes":42,"downloadUrl":"http://127.0.0.1:19100/a"
		}]
	}`)
	var request Request
	if err := json.Unmarshal(data, &request); err != nil {
		t.Fatal(err)
	}
	if request.FileName != "ui.png" || len(request.Attachments) != 1 {
		t.Fatalf("request = %#v", request)
	}
	if request.Attachments[0].ID != "ATT_1" || request.Attachments[0].SizeBytes != 42 {
		t.Fatalf("attachment = %#v", request.Attachments[0])
	}
}

func TestRequestParsesLegacyFileName(t *testing.T) {
	t.Parallel()
	var request Request
	if err := json.Unmarshal([]byte(`{"text":"test","file_name":"legacy.png"}`), &request); err != nil {
		t.Fatal(err)
	}
	if request.FileName != "legacy.png" {
		t.Fatalf("FileName = %q", request.FileName)
	}
}
