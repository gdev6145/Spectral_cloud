package agentgroup

import (
	"testing"

	"github.com/gdev6145/Spectral_cloud/pkg/store"
)

const testTenant = "acme"

func TestCreateList(t *testing.T) {
	m := New()
	g, err := m.Create(testTenant, "workers")
	if err != nil {
		t.Fatal(err)
	}
	if g.ID == "" || g.Name != "workers" || g.Tenant != testTenant {
		t.Errorf("unexpected group: %+v", g)
	}
	list := m.List(testTenant)
	if len(list) != 1 {
		t.Errorf("expected 1, got %d", len(list))
	}
	// Other tenant sees nothing.
	if other := m.List("other"); len(other) != 0 {
		t.Errorf("expected 0 for other tenant, got %d", len(other))
	}
}

func TestCreateEmptyName(t *testing.T) {
	m := New()
	_, err := m.Create(testTenant, "")
	if err == nil {
		t.Fatal("expected error for empty name")
	}
}

func TestDelete(t *testing.T) {
	m := New()
	g, _ := m.Create(testTenant, "g1")
	if !m.Delete(testTenant, g.ID) {
		t.Fatal("expected true")
	}
	if m.Delete(testTenant, g.ID) {
		t.Fatal("expected false on second delete")
	}
	// Wrong tenant cannot delete.
	g2, _ := m.Create(testTenant, "g2")
	if m.Delete("other", g2.ID) {
		t.Fatal("wrong tenant should not be able to delete")
	}
}

func TestUpdate(t *testing.T) {
	m := New()
	g, _ := m.Create(testTenant, "g1")
	name := "workers"
	updated, ok, err := m.Update(testTenant, g.ID, UpdateParams{Name: &name})
	if err != nil {
		t.Fatalf("update group: %v", err)
	}
	if !ok {
		t.Fatal("expected group to exist")
	}
	if updated.Name != name {
		t.Fatalf("unexpected updated group: %+v", updated)
	}
}

func TestUpdateValidationAndTenantScope(t *testing.T) {
	m := New()
	g, _ := m.Create(testTenant, "g1")
	empty := ""
	if _, ok, err := m.Update(testTenant, g.ID, UpdateParams{Name: &empty}); err == nil || !ok {
		t.Fatal("expected validation error for empty name")
	}
	if _, ok, err := m.Update("other", g.ID, UpdateParams{}); err != nil || ok {
		t.Fatal("wrong tenant should not update group")
	}
}

func TestAddRemoveMember(t *testing.T) {
	m := New()
	g, _ := m.Create(testTenant, "g1")
	if err := m.AddMemberWithWeight(testTenant, g.ID, "agent-1", 2); err != nil {
		t.Fatal(err)
	}
	if err := m.AddMember(testTenant, g.ID, "agent-1"); err != nil {
		t.Fatal("second add should be no-op, not error")
	}
	snap, _ := m.Get(testTenant, g.ID)
	if len(snap.Members) != 1 {
		t.Errorf("expected 1 member, got %d", len(snap.Members))
	}
	if snap.Members[0].Weight != 2 {
		t.Fatalf("expected weight 2, got %+v", snap.Members[0])
	}
	if err := m.RemoveMember(testTenant, g.ID, "agent-1"); err != nil {
		t.Fatal(err)
	}
	snap2, _ := m.Get(testTenant, g.ID)
	if len(snap2.Members) != 0 {
		t.Errorf("expected 0 members after remove, got %d", len(snap2.Members))
	}
}

func TestUpdateMemberWeight(t *testing.T) {
	m := New()
	g, _ := m.Create(testTenant, "g1")
	_ = m.AddMember(testTenant, g.ID, "agent-1")
	updated, ok, err := m.UpdateMemberWeight(testTenant, g.ID, "agent-1", 3)
	if err != nil {
		t.Fatalf("update member weight: %v", err)
	}
	if !ok {
		t.Fatal("expected member update to succeed")
	}
	if updated.Members[0].Weight != 3 {
		t.Fatalf("expected updated weight, got %+v", updated.Members[0])
	}
}

func TestUpdateMemberWeightValidationAndTenantScope(t *testing.T) {
	m := New()
	g, _ := m.Create(testTenant, "g1")
	_ = m.AddMember(testTenant, g.ID, "agent-1")
	if _, ok, err := m.UpdateMemberWeight(testTenant, g.ID, "agent-1", 0); err == nil || !ok {
		t.Fatal("expected validation error for zero weight")
	}
	if _, ok, err := m.UpdateMemberWeight("other", g.ID, "agent-1", 2); err != nil || ok {
		t.Fatal("wrong tenant should not update member weight")
	}
}

func TestNextRoundRobin(t *testing.T) {
	m := New()
	g, _ := m.Create(testTenant, "g1")
	_ = m.AddMember(testTenant, g.ID, "a1")
	_ = m.AddMember(testTenant, g.ID, "a2")
	_ = m.AddMember(testTenant, g.ID, "a3")

	got := map[string]int{}
	for i := 0; i < 9; i++ {
		id, ok := m.Next(testTenant, g.ID, nil)
		if !ok {
			t.Fatal("expected ok")
		}
		got[id]++
	}
	for _, id := range []string{"a1", "a2", "a3"} {
		if got[id] != 3 {
			t.Errorf("expected 3 for %s, got %d", id, got[id])
		}
	}
}

func TestNextWeightedRoundRobin(t *testing.T) {
	m := New()
	g, _ := m.Create(testTenant, "g1")
	_ = m.AddMemberWithWeight(testTenant, g.ID, "a1", 2)
	_ = m.AddMemberWithWeight(testTenant, g.ID, "a2", 1)
	got := map[string]int{}
	for i := 0; i < 6; i++ {
		id, ok := m.Next(testTenant, g.ID, nil)
		if !ok {
			t.Fatal("expected ok")
		}
		got[id]++
	}
	if got["a1"] != 4 || got["a2"] != 2 {
		t.Fatalf("expected weighted distribution 4:2, got %+v", got)
	}
}

func TestNextWithAllowFn(t *testing.T) {
	m := New()
	g, _ := m.Create(testTenant, "g1")
	_ = m.AddMember(testTenant, g.ID, "a1")
	_ = m.AddMember(testTenant, g.ID, "a2")

	// Block a1, only a2 should be returned.
	for i := 0; i < 4; i++ {
		id, ok := m.Next(testTenant, g.ID, func(agentID string) bool { return agentID != "a1" })
		if !ok || id != "a2" {
			t.Errorf("expected a2, got %s (ok=%v)", id, ok)
		}
	}
}

func TestNextEmptyGroup(t *testing.T) {
	m := New()
	g, _ := m.Create(testTenant, "empty")
	_, ok := m.Next(testTenant, g.ID, nil)
	if ok {
		t.Error("expected false for empty group")
	}
}

func TestNextAllBlocked(t *testing.T) {
	m := New()
	g, _ := m.Create(testTenant, "g1")
	_ = m.AddMember(testTenant, g.ID, "a1")
	_, ok := m.Next(testTenant, g.ID, func(string) bool { return false })
	if ok {
		t.Error("expected false when all blocked")
	}
}

func TestNextWrongTenant(t *testing.T) {
	m := New()
	g, _ := m.Create(testTenant, "g1")
	_ = m.AddMember(testTenant, g.ID, "a1")
	_, ok := m.Next("other", g.ID, nil)
	if ok {
		t.Error("expected false for wrong tenant")
	}
}

func TestAddMemberUnknownGroup(t *testing.T) {
	m := New()
	err := m.AddMember(testTenant, "nope", "agent-1")
	if err == nil {
		t.Fatal("expected error for unknown group")
	}
}

func TestAddMemberWithInvalidWeight(t *testing.T) {
	m := New()
	g, _ := m.Create(testTenant, "g1")
	if err := m.AddMemberWithWeight(testTenant, g.ID, "agent-1", 0); err == nil {
		t.Fatal("expected error for invalid weight")
	}
}

func TestRemoveMissingMember(t *testing.T) {
	m := New()
	g, _ := m.Create(testTenant, "g1")
	if err := m.RemoveMember(testTenant, g.ID, "missing"); err == nil {
		t.Fatal("expected error for missing member")
	}
}

func TestLoadFromStoreRestoresGroups(t *testing.T) {
	tmp := t.TempDir()
	db, err := store.Open(store.DBPath(tmp))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	m1 := NewWithStore(db)
	g, err := m1.Create(testTenant, "workers")
	if err != nil {
		t.Fatalf("create group: %v", err)
	}
	if err := m1.AddMemberWithWeight(testTenant, g.ID, "agent-1", 2); err != nil {
		t.Fatalf("add weighted member: %v", err)
	}

	m2 := NewWithStore(db)
	n, err := m2.LoadFromStore(testTenant)
	if err != nil {
		t.Fatalf("load groups: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 restored group, got %d", n)
	}
	got, ok := m2.Get(testTenant, g.ID)
	if !ok {
		t.Fatal("expected restored group")
	}
	if got.Name != "workers" || len(got.Members) != 1 || got.Members[0].Weight != 2 {
		t.Fatalf("unexpected restored group: %+v", got)
	}
}

func TestDeleteRemovesPersistedGroup(t *testing.T) {
	tmp := t.TempDir()
	db, err := store.Open(store.DBPath(tmp))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	m1 := NewWithStore(db)
	g, err := m1.Create(testTenant, "workers")
	if err != nil {
		t.Fatalf("create group: %v", err)
	}
	if !m1.Delete(testTenant, g.ID) {
		t.Fatal("expected delete to succeed")
	}

	m2 := NewWithStore(db)
	n, err := m2.LoadFromStore(testTenant)
	if err != nil {
		t.Fatalf("load groups: %v", err)
	}
	if n != 0 {
		t.Fatalf("expected 0 restored groups, got %d", n)
	}
}
