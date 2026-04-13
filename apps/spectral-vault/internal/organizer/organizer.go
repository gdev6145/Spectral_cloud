package organizer

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Input holds the metadata used to derive a canonical name and folder.
type Input struct {
	OriginalName string
	MimeType     string
	Size         int64
	UploadedAt   time.Time
}

// Result holds the suggested canonical name and folder for a file.
type Result struct {
	CanonicalName string
	FolderPath    string
	Tags          []string
	Summary       string
}

// LLMHook is an optional interface for plugging in an LLM-based organizer.
// If not set, only heuristics are used.
type LLMHook interface {
	Enhance(in Input, heuristic Result) (Result, error)
}

// Organizer applies heuristic (and optionally LLM-based) rules to suggest file organisation.
type Organizer struct {
	hook LLMHook
}

// New creates a new Organizer.
func New() *Organizer {
	return &Organizer{}
}

// SetLLMHook registers an LLMHook. Pass nil to disable.
func (o *Organizer) SetLLMHook(hook LLMHook) {
	o.hook = hook
}

// Organize analyses file metadata and returns a suggested canonical name and folder.
func (o *Organizer) Organize(in Input) Result {
	res := heuristic(in)
	if o.hook != nil {
		if enhanced, err := o.hook.Enhance(in, res); err == nil {
			return enhanced
		}
	}
	return res
}

// nonAlnum matches any character that is not a letter, digit, or dot.
var nonAlnum = regexp.MustCompile(`[^a-z0-9.]+`)

// cleanName lowercases and normalises a filename for use in canonical names.
func cleanName(name string) string {
	lower := strings.ToLower(name)
	clean := nonAlnum.ReplaceAllString(lower, "_")
	clean = strings.Trim(clean, "_")
	return clean
}

func heuristic(in Input) Result {
	datePrefix := in.UploadedAt.Format("2006-01-02")
	canonicalName := datePrefix + "_" + cleanName(in.OriginalName)

	mime := strings.ToLower(in.MimeType)
	nameLower := strings.ToLower(in.OriginalName)

	// Determine folder from MIME type.
	folder := mimeFolder(mime)
	tags := mimeTags(mime)

	// Keyword-based folder and tag overrides.
	folder, tags = applyKeywords(nameLower, folder, tags)

	// Build human-readable summary.
	summary := buildSummary(mime, datePrefix)

	return Result{
		CanonicalName: canonicalName,
		FolderPath:    folder,
		Tags:          tags,
		Summary:       summary,
	}
}

func mimeFolder(mime string) string {
	switch {
	case strings.HasPrefix(mime, "image/"):
		return "media/images"
	case strings.HasPrefix(mime, "video/"):
		return "media/videos"
	case strings.HasPrefix(mime, "audio/"):
		return "media/audio"
	case mime == "application/pdf":
		return "documents/pdf"
	case strings.HasPrefix(mime, "text/") || mime == "application/json":
		return "documents/text"
	case mime == "application/zip" || mime == "application/x-tar":
		return "archives"
	case mime == "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet" || mime == "text/csv":
		return "data/spreadsheets"
	default:
		return "misc"
	}
}

func mimeTags(mime string) []string {
	switch {
	case strings.HasPrefix(mime, "image/"):
		return []string{"image"}
	case strings.HasPrefix(mime, "video/"):
		return []string{"video"}
	case strings.HasPrefix(mime, "audio/"):
		return []string{"audio"}
	case mime == "application/pdf":
		return []string{"document", "pdf"}
	case strings.HasPrefix(mime, "text/") || mime == "application/json":
		return []string{"document", "text"}
	case mime == "application/zip" || mime == "application/x-tar":
		return []string{"archive"}
	case mime == "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet" || mime == "text/csv":
		return []string{"data", "spreadsheet"}
	default:
		return []string{"misc"}
	}
}

type keywordRule struct {
	keywords []string
	folder   string
	tag      string
}

var keywordRules = []keywordRule{
	{keywords: []string{"invoice", "receipt"}, folder: "documents/invoices", tag: "invoice"},
	{keywords: []string{"contract", "agreement"}, folder: "documents/contracts", tag: "contract"},
	{keywords: []string{"report"}, folder: "documents/reports", tag: "report"},
	{keywords: []string{"photo", "screenshot"}, folder: "media/images", tag: "image"},
	{keywords: []string{"backup", "dump", "export"}, folder: "archives", tag: "archive"},
}

func applyKeywords(nameLower, folder string, tags []string) (string, []string) {
	for _, rule := range keywordRules {
		for _, kw := range rule.keywords {
			if strings.Contains(nameLower, kw) {
				folder = rule.folder
				// Add tag if not already present.
				found := false
				for _, t := range tags {
					if t == rule.tag {
						found = true
						break
					}
				}
				if !found {
					tags = append(tags, rule.tag)
				}
				return folder, tags
			}
		}
	}
	return folder, tags
}

func buildSummary(mime, date string) string {
	var category string
	switch {
	case strings.HasPrefix(mime, "image/"):
		category = "Image"
	case strings.HasPrefix(mime, "video/"):
		category = "Video"
	case strings.HasPrefix(mime, "audio/"):
		category = "Audio"
	case mime == "application/pdf":
		category = "PDF document"
	case strings.HasPrefix(mime, "text/") || mime == "application/json":
		category = "Text document"
	case mime == "application/zip" || mime == "application/x-tar":
		category = "Archive"
	case mime == "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet" || mime == "text/csv":
		category = "Spreadsheet"
	default:
		category = "File"
	}
	return fmt.Sprintf("%s uploaded on %s", category, date)
}
