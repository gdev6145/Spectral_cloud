package db

import (
	"testing"
	"time"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	d, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func sampleRecord(id, tenant string) FileRecord {
	now := time.Now().UTC().Truncate(time.Second)
	return FileRecord{
		ID:            id,
		Tenant:        tenant,
		OriginalName:  "invoice_scan.pdf",
		CanonicalName: "2026-04-13_invoice_scan.pdf",
		FolderPath:    "documents/invoices",
		StorageKey:    tenant + "/" + id + "/invoice_scan.pdf",
		MimeType:      "application/pdf",
		Size:          1024,
		SHA256:        "abc123" + id,
		Tags:          "document,invoice",
		Summary:       "PDF document uploaded on 2026-04-13",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
}

func TestInsertAndGet(t *testing.T) {
	d := openTestDB(t)
	rec := sampleRecord("vf-00000001", "tenant1")

	if err := d.Insert(rec); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	got, err := d.GetByID("tenant1", "vf-00000001")
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got == nil {
		t.Fatal("GetByID: expected record, got nil")
	}
	if got.ID != rec.ID {
		t.Errorf("ID: expected %q, got %q", rec.ID, got.ID)
	}
	if got.OriginalName != rec.OriginalName {
		t.Errorf("OriginalName: expected %q, got %q", rec.OriginalName, got.OriginalName)
	}
	if got.FolderPath != rec.FolderPath {
		t.Errorf("FolderPath: expected %q, got %q", rec.FolderPath, got.FolderPath)
	}

	// Wrong tenant should return nil.
	missing, err := d.GetByID("other", "vf-00000001")
	if err != nil {
		t.Fatalf("GetByID wrong tenant: %v", err)
	}
	if missing != nil {
		t.Fatalf("expected nil for wrong tenant, got %+v", missing)
	}
}

func TestSearch(t *testing.T) {
	d := openTestDB(t)
	r1 := sampleRecord("vf-00000001", "tenant1")
	r2 := sampleRecord("vf-00000002", "tenant1")
	r2.OriginalName = "contract_2026.docx"
	r2.CanonicalName = "2026-04-13_contract_2026.docx"
	r2.Tags = "document,contract"
	r2.SHA256 = "def456"

	for _, r := range []FileRecord{r1, r2} {
		if err := d.Insert(r); err != nil {
			t.Fatalf("Insert: %v", err)
		}
	}

	results, err := d.Search("tenant1", "invoice")
	if err != nil {
		t.Fatalf("Search invoice: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result for 'invoice', got %d", len(results))
	}

	results, err = d.Search("tenant1", "contract")
	if err != nil {
		t.Fatalf("Search contract: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result for 'contract', got %d", len(results))
	}

	results, err = d.Search("tenant1", "document")
	if err != nil {
		t.Fatalf("Search document: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results for 'document', got %d", len(results))
	}

	// Different tenant should get no results.
	results, err = d.Search("other", "invoice")
	if err != nil {
		t.Fatalf("Search other tenant: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results for wrong tenant, got %d", len(results))
	}
}

func TestListFolders(t *testing.T) {
	d := openTestDB(t)
	folders := []string{"documents/invoices", "documents/contracts", "media/images", "archives"}
	for i, fp := range folders {
		r := sampleRecord("vf-0000000"+string(rune('1'+i)), "tenant1")
		r.FolderPath = fp
		r.SHA256 = "hash" + string(rune('a'+i))
		if err := d.Insert(r); err != nil {
			t.Fatalf("Insert: %v", err)
		}
	}

	all, err := d.ListFolders("tenant1", "")
	if err != nil {
		t.Fatalf("ListFolders all: %v", err)
	}
	if len(all) != 4 {
		t.Fatalf("expected 4 folders, got %d: %v", len(all), all)
	}

	docs, err := d.ListFolders("tenant1", "documents")
	if err != nil {
		t.Fatalf("ListFolders documents: %v", err)
	}
	if len(docs) != 2 {
		t.Fatalf("expected 2 document folders, got %d: %v", len(docs), docs)
	}

	// Different tenant: no folders.
	none, err := d.ListFolders("other", "")
	if err != nil {
		t.Fatalf("ListFolders other: %v", err)
	}
	if len(none) != 0 {
		t.Fatalf("expected 0 folders for wrong tenant, got %d", len(none))
	}
}

func TestGetBySHA256(t *testing.T) {
	d := openTestDB(t)
	rec := sampleRecord("vf-00000001", "tenant1")
	if err := d.Insert(rec); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	got, err := d.GetBySHA256("tenant1", rec.SHA256)
	if err != nil {
		t.Fatalf("GetBySHA256: %v", err)
	}
	if got == nil {
		t.Fatal("GetBySHA256: expected record, got nil")
	}
	if got.ID != rec.ID {
		t.Errorf("ID: expected %q, got %q", rec.ID, got.ID)
	}

	// Unknown hash returns nil.
	missing, err := d.GetBySHA256("tenant1", "nonexistent")
	if err != nil {
		t.Fatalf("GetBySHA256 missing: %v", err)
	}
	if missing != nil {
		t.Fatalf("expected nil for unknown sha256, got %+v", missing)
	}

	// Different tenant should not find the record.
	other, err := d.GetBySHA256("other", rec.SHA256)
	if err != nil {
		t.Fatalf("GetBySHA256 other tenant: %v", err)
	}
	if other != nil {
		t.Fatalf("expected nil for wrong tenant, got %+v", other)
	}
}

func TestUpdate(t *testing.T) {
	d := openTestDB(t)
	rec := sampleRecord("vf-00000001", "tenant1")
	if err := d.Insert(rec); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	rec.CanonicalName = "2026-04-13_updated_invoice.pdf"
	rec.FolderPath = "documents/contracts"
	rec.Tags = "document,updated"
	rec.Summary = "Updated summary"
	rec.UpdatedAt = time.Now().UTC().Truncate(time.Second)

	if err := d.Update(rec); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, err := d.GetByID("tenant1", "vf-00000001")
	if err != nil {
		t.Fatalf("GetByID after update: %v", err)
	}
	if got.CanonicalName != rec.CanonicalName {
		t.Errorf("CanonicalName: expected %q, got %q", rec.CanonicalName, got.CanonicalName)
	}
	if got.FolderPath != rec.FolderPath {
		t.Errorf("FolderPath: expected %q, got %q", rec.FolderPath, got.FolderPath)
	}
	if got.Tags != rec.Tags {
		t.Errorf("Tags: expected %q, got %q", rec.Tags, got.Tags)
	}
	if got.Summary != rec.Summary {
		t.Errorf("Summary: expected %q, got %q", rec.Summary, got.Summary)
	}
}
