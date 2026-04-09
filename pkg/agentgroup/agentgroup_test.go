package agentgroup

import (
	"testing"
)

func TestCreateList(t *testing.T) {
	m := New()
	g, err := m.Create("workers")
	if err != nil {
		t.Fatal(err)
	}
	if g.ID == "" || g.Name != "workers" {
		t.Errorf("unexpected group: %+v", g)
	}
	list := m.List()
	if len(list) != 1 {
		t.Errorf("expected 1, got %d", len(list))
	}
}

func TestCreateEmptyName(t *testing.T) {
	m := New()
	_, err := m.Create("")
	if err == nil {
		t.Fatal("expected error for empty name")
	}
}

func TestDelete(t *testing.T) {
	m := New()
	g, _ := m.Create("g1")
	if !m.Delete(g.ID) {
		t.Fatal("expected true")
	}
	if m.Delete(g.ID) {
		t.Fatal("expected false on second delete")
	}
}

func TestAddRemoveMember(t *testing.T) {
	m := New()
	g, _ := m.Create("g1")
	if err := m.AddMember(g.ID, "agent-1"); err != nil {
		t.Fatal(err)
	}
	if err := m.AddMember(g.ID, "agent-1"); err != nil {
		t.Fatal("second add should be no-op, not error")
	}
	snap, _ := m.Get(g.ID)
	if len(snap.Members) != 1 {
		t.Errorf("expected 1 member, got %d", len(snap.Members))
	}
	if err := m.RemoveMember(g.ID, "agent-1"); err != nil {
		t.Fatal(err)
	}
	snap2, _ := m.Get(g.ID)
	if len(snap2.Members) != 0 {
		t.Errorf("expected 0 members after remove, got %d", len(snap2.Members))
	}
}

func TestNextRoundRobin(t *testing.T) {
	m := New()
	g, _ := m.Create("g1")
	_ = m.AddMember(g.ID, "a1")
	_ = m.AddMember(g.ID, "a2")
	_ = m.AddMember(g.ID, "a3")

	got := map[string]int{}
	for i := 0; i < 9; i++ {
		id, ok := m.Next(g.ID, nil)
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
	g, _ := m.Create("g1")
	_ = m.AddMember(g.ID, "a1")
	_ = m.AddMember(g.ID, "a2")

	// Block a1, only a2 should be returned.
	for i := 0; i < 4; i++ {
		id, ok := m.Next(g.ID, func(agentID string) bool { return agentID != "a1" })
		if !ok || id != "a2" {
			t.Errorf("expected a2, got %s (ok=%v)", id, ok)
		}
	}
}

func TestNextEmptyGroup(t *testing.T) {
	m := New()
	g, _ := m.Create("empty")
	_, ok := m.Next(g.ID, nil)
	if ok {
		t.Error("expected false for empty group")
	}
}

func TestNextAllBlocked(t *testing.T) {
	m := New()
	g, _ := m.Create("g1")
	_ = m.AddMember(g.ID, "a1")
	_, ok := m.Next(g.ID, func(string) bool { return false })
	if ok {
		t.Error("expected false when all blocked")
	}
}

func TestAddMemberUnknownGroup(t *testing.T) {
	m := New()
	err := m.AddMember("nope", "agent-1")
	if err == nil {
		t.Fatal("expected error for unknown group")
	}
}
