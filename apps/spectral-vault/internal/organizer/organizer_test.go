package organizer

import (
	"strings"
	"testing"
	"time"
)

var uploadedAt = time.Date(2026, 4, 13, 10, 0, 0, 0, time.UTC)

func TestOrganizePDF(t *testing.T) {
	o := New()
	res := o.Organize(Input{
		OriginalName: "report.pdf",
		MimeType:     "application/pdf",
		Size:         2048,
		UploadedAt:   uploadedAt,
	})
	if res.FolderPath != "documents/reports" {
		t.Errorf("FolderPath: expected %q, got %q", "documents/reports", res.FolderPath)
	}
	if !strings.HasPrefix(res.CanonicalName, "2026-04-13_") {
		t.Errorf("CanonicalName: expected date prefix, got %q", res.CanonicalName)
	}
	if !strings.Contains(res.Summary, "2026-04-13") {
		t.Errorf("Summary should contain date, got %q", res.Summary)
	}
	if !containsTag(res.Tags, "pdf") {
		t.Errorf("Tags should include 'pdf', got %v", res.Tags)
	}
}

func TestOrganizeImage(t *testing.T) {
	o := New()
	res := o.Organize(Input{
		OriginalName: "sunset.jpg",
		MimeType:     "image/jpeg",
		Size:         512000,
		UploadedAt:   uploadedAt,
	})
	if res.FolderPath != "media/images" {
		t.Errorf("FolderPath: expected %q, got %q", "media/images", res.FolderPath)
	}
	if !containsTag(res.Tags, "image") {
		t.Errorf("Tags should include 'image', got %v", res.Tags)
	}
}

func TestOrganizeVideo(t *testing.T) {
	o := New()
	res := o.Organize(Input{
		OriginalName: "recording.mp4",
		MimeType:     "video/mp4",
		Size:         10000000,
		UploadedAt:   uploadedAt,
	})
	if res.FolderPath != "media/videos" {
		t.Errorf("FolderPath: expected %q, got %q", "media/videos", res.FolderPath)
	}
	if !containsTag(res.Tags, "video") {
		t.Errorf("Tags should include 'video', got %v", res.Tags)
	}
	if !strings.Contains(res.Summary, "Video") {
		t.Errorf("Summary should mention Video, got %q", res.Summary)
	}
}

func TestOrganizeInvoice(t *testing.T) {
	o := New()
	res := o.Organize(Input{
		OriginalName: "invoice_acme_april.pdf",
		MimeType:     "application/pdf",
		Size:         1024,
		UploadedAt:   uploadedAt,
	})
	if res.FolderPath != "documents/invoices" {
		t.Errorf("FolderPath: expected %q, got %q", "documents/invoices", res.FolderPath)
	}
	if !containsTag(res.Tags, "invoice") {
		t.Errorf("Tags should include 'invoice', got %v", res.Tags)
	}
}

func TestOrganizeUnknown(t *testing.T) {
	o := New()
	res := o.Organize(Input{
		OriginalName: "mystery.xyz",
		MimeType:     "application/octet-stream",
		Size:         100,
		UploadedAt:   uploadedAt,
	})
	if res.FolderPath != "misc" {
		t.Errorf("FolderPath: expected %q, got %q", "misc", res.FolderPath)
	}
	if !containsTag(res.Tags, "misc") {
		t.Errorf("Tags should include 'misc', got %v", res.Tags)
	}
}

func TestCanonicalNameFormat(t *testing.T) {
	o := New()
	cases := []struct {
		name string
		mime string
	}{
		{"My Invoice 2026.pdf", "application/pdf"},
		{"Photo Upload.jpeg", "image/jpeg"},
		{"backup dump.zip", "application/zip"},
		{"  weird   name!@#.txt", "text/plain"},
	}
	for _, tc := range cases {
		res := o.Organize(Input{
			OriginalName: tc.name,
			MimeType:     tc.mime,
			UploadedAt:   uploadedAt,
		})
		if !strings.HasPrefix(res.CanonicalName, "2026-04-13_") {
			t.Errorf("CanonicalName for %q: must start with date prefix, got %q", tc.name, res.CanonicalName)
		}
	}
}

func containsTag(tags []string, want string) bool {
	for _, t := range tags {
		if t == want {
			return true
		}
	}
	return false
}
