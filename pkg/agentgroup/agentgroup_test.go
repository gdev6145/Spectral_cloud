package agentgroup

import (
	"testing"
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

func TestAddRemoveMember(t *testing.T) {
	m := New()
	g, _ := m.Create(testTenant, "g1")
	if err := m.AddMember(testTenant, g.ID, "agent-1"); err != nil {
		t.Fatal(err)
	}
	if err := m.AddMember(testTenant, g.ID, "agent-1"); err != nil {
		t.Fatal("second add should be no-op, not error")
	}
	snap, _ := m.Get(testTenant, g.ID)
	if len(snap.Members) != 1 {
		t.Errorf("expected 1 member, got %d", len(snap.Members))
	}
	if err := m.RemoveMember(testTenant, g.ID, "agent-1"); err != nil {
		t.Fatal(err)
	}
	snap2, _ := m.Get(testTenant, g.ID)
	if len(snap2.Members) != 0 {
		t.Errorf("expected 0 members after remove, got %d", len(snap2.Members))
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
