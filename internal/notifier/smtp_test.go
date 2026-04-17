package notifier

import (
	"strings"
	"testing"
	"time"

	"github.com/balli/aws-s3-gda-cleaner/internal/scanner"
)

func TestFormatSize(t *testing.T) {
	tests := []struct {
		name string
		in   int64
		want string
	}{
		{name: "zero", in: 0, want: "0 B"},
		{name: "bytes", in: 12, want: "12 B"},
		{name: "one kib", in: 1024, want: "1.0 KiB"},
		{name: "fractional kib", in: 1536, want: "1.5 KiB"},
		{name: "one mib", in: 1024 * 1024, want: "1.0 MiB"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := formatSize(tc.in)
			if got != tc.want {
				t.Fatalf("formatSize(%d) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestFormatAge(t *testing.T) {
	tests := []struct {
		name         string
		in           time.Duration
		wantContains string
	}{
		{name: "hours", in: 2 * time.Hour, wantContains: "2 hours ago"},
		{name: "days", in: 48 * time.Hour, wantContains: "2 days ago"},
		{name: "years plus days", in: 400 * 24 * time.Hour, wantContains: "year ago"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := formatAge(tc.in)
			if !strings.Contains(got, tc.wantContains) {
				t.Fatalf("formatAge(%v) = %q, expected to contain %q", tc.in, got, tc.wantContains)
			}
		})
	}
}

func TestBuildTemplateDataSortsAndTotals(t *testing.T) {
	candidates := []scanner.S3Object{
		{Key: "z-last.txt", Size: 20, LastModified: time.Now().Add(-48 * time.Hour)},
		{Key: "a-first.txt", Size: 10, LastModified: time.Now().Add(-24 * time.Hour)},
	}

	data := buildTemplateData(candidates)

	if data["FileCount"].(int) != 2 {
		t.Fatalf("FileCount = %v, want 2", data["FileCount"])
	}
	if data["TotalSize"].(int64) != 30 {
		t.Fatalf("TotalSize = %v, want 30", data["TotalSize"])
	}

	files := data["Files"].([]fileEntry)
	if len(files) != 2 {
		t.Fatalf("files len = %d, want 2", len(files))
	}
	if files[0].Name != "a-first.txt" || files[1].Name != "z-last.txt" {
		t.Fatalf("files not sorted by key: %+v", files)
	}
}

func TestRenderTemplate(t *testing.T) {
	valid, err := renderTemplate("hello {{.Name}}", map[string]any{"Name": "world"})
	if err != nil {
		t.Fatalf("renderTemplate valid template error: %v", err)
	}
	if valid != "hello world" {
		t.Fatalf("rendered = %q, want %q", valid, "hello world")
	}

	_, err = renderTemplate("{{", map[string]any{})
	if err == nil {
		t.Fatal("expected template parse error")
	}
}

func TestSummaryTemplateIncludesApprovalLink(t *testing.T) {
	candidates := []scanner.S3Object{{
		Key:          "x.txt",
		Size:         12,
		LastModified: time.Now().Add(-48 * time.Hour),
	}}

	data := buildTemplateData(candidates)
	data["HasApprovalURL"] = true
	data["ApprovalURL"] = "https://example.com/approve/token123"

	body, err := renderTemplate(summaryTemplate, data)
	if err != nil {
		t.Fatalf("summary template render error: %v", err)
	}
	if !strings.Contains(body, "https://example.com/approve/token123") {
		t.Fatalf("summary body missing approval link: %s", body)
	}
}
