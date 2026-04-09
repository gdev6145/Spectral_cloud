package blockchain

import "testing"

func TestBlockchainHeightAndAdd(t *testing.T) {
	bc := NewBlockchain()
	if got := bc.Height(); got != 1 {
		t.Fatalf("expected genesis height 1, got %d", got)
	}
	bc.AddBlock([]Transaction{{Sender: "a", Recipient: "b", Amount: 1}})
	if got := bc.Height(); got != 2 {
		t.Fatalf("expected height 2 after add, got %d", got)
	}
}

func TestGetBlock(t *testing.T) {
	bc := NewBlockchain()
	genesis, ok := bc.GetBlock(0)
	if !ok {
		t.Fatal("expected genesis block at index 0")
	}
	if genesis.Index != 0 {
		t.Fatalf("expected index 0, got %d", genesis.Index)
	}

	_, ok = bc.GetBlock(-1)
	if ok {
		t.Fatal("expected false for negative index")
	}
	_, ok = bc.GetBlock(999)
	if ok {
		t.Fatal("expected false for out-of-range index")
	}

	bc.AddBlock([]Transaction{{Sender: "a", Recipient: "b", Amount: 1}})
	b1, ok := bc.GetBlock(1)
	if !ok {
		t.Fatal("expected block at index 1")
	}
	if b1.Index != 1 {
		t.Fatalf("expected index 1, got %d", b1.Index)
	}
}

func TestBlockRange(t *testing.T) {
	bc := NewBlockchain()
	bc.AddBlock([]Transaction{{Sender: "a", Recipient: "b", Amount: 1}})
	bc.AddBlock([]Transaction{{Sender: "b", Recipient: "c", Amount: 2}})

	// [0,3) -> all 3 blocks
	all := bc.BlockRange(0, 3)
	if len(all) != 3 {
		t.Fatalf("expected 3 blocks, got %d", len(all))
	}

	// clamping: end beyond length
	clamped := bc.BlockRange(1, 100)
	if len(clamped) != 2 {
		t.Fatalf("expected 2 blocks (clamped), got %d", len(clamped))
	}

	// empty range
	empty := bc.BlockRange(2, 2)
	if empty != nil {
		t.Fatalf("expected nil for empty range, got %v", empty)
	}

	// negative start clamped to 0
	fromStart := bc.BlockRange(-5, 2)
	if len(fromStart) != 2 {
		t.Fatalf("expected 2 blocks for negative start, got %d", len(fromStart))
	}
}

func TestBlockSignature(t *testing.T) {
	bc := NewBlockchain()
	b := bc.AddBlock([]Transaction{{Sender: "a", Recipient: "b", Amount: 1}})

	// Unsigned block — VerifySignature should return false.
	if VerifySignature(b, "secret") {
		t.Fatal("unsigned block should not verify")
	}

	sig := SignBlock(b, "secret")
	if sig == "" {
		t.Fatal("expected non-empty signature")
	}
	if b.Signature != sig {
		t.Fatal("block.Signature should match returned sig")
	}
	if !VerifySignature(b, "secret") {
		t.Fatal("expected valid signature to verify")
	}
	if VerifySignature(b, "wrong-key") {
		t.Fatal("expected signature to fail with wrong key")
	}
}

func TestSignBlockNilSafe(t *testing.T) {
	if got := SignBlock(nil, "key"); got != "" {
		t.Fatalf("expected empty string for nil block, got %q", got)
	}
	if VerifySignature(nil, "key") {
		t.Fatal("expected false for nil block")
	}
}

func TestAddSignedBlock(t *testing.T) {
	bc := NewBlockchain()
	b := bc.AddSignedBlock([]Transaction{{Sender: "x", Recipient: "y", Amount: 5}}, "signing-key")
	if b.Signature == "" {
		t.Fatal("expected non-empty signature from AddSignedBlock")
	}
	if !VerifySignature(b, "signing-key") {
		t.Fatal("expected signed block to verify")
	}
}

func TestAddSignedBlockNoKey(t *testing.T) {
	bc := NewBlockchain()
	b := bc.AddSignedBlock([]Transaction{{Sender: "x", Recipient: "y", Amount: 1}}, "")
	if b.Signature != "" {
		t.Fatal("expected no signature when no key provided")
	}
}

func TestVerify(t *testing.T) {
	bc := NewBlockchain()
	b := bc.AddBlock([]Transaction{{Sender: "a", Recipient: "b", Amount: 5}})
	if !Verify(b) {
		t.Fatal("expected valid block to verify")
	}
	bad := *b
	bad.Hash = "bad"
	if Verify(&bad) {
		t.Fatal("expected tampered block to fail verification")
	}
	if Verify(nil) {
		t.Fatal("expected nil block to fail verification")
	}
}

func TestVerifyChainValid(t *testing.T) {
	bc := NewBlockchain()
	bc.AddBlock([]Transaction{{Sender: "a", Recipient: "b", Amount: 1}})
	bc.AddBlock([]Transaction{{Sender: "b", Recipient: "c", Amount: 2}})

	idx, ok := bc.VerifyChain()
	if !ok {
		t.Fatalf("expected valid chain, first bad index reported: %d", idx)
	}
	if idx != -1 {
		t.Fatalf("expected idx=-1 for valid chain, got %d", idx)
	}
}

func TestVerifyChainTamperedHash(t *testing.T) {
	bc := NewBlockchain()
	bc.AddBlock([]Transaction{{Sender: "a", Recipient: "b", Amount: 1}})

	// Tamper the genesis block hash directly (bypass the lock for test access).
	bc.blocks[0].Hash = "deadbeef"

	idx, ok := bc.VerifyChain()
	if ok {
		t.Fatal("expected chain to be invalid after tamper")
	}
	if idx != 0 {
		t.Fatalf("expected first bad index=0, got %d", idx)
	}
}

func TestVerifyChainBrokenLink(t *testing.T) {
	bc := NewBlockchain()
	bc.AddBlock([]Transaction{{Sender: "a", Recipient: "b", Amount: 1}})

	// Break the PreviousHash link without touching the hash calculation.
	bc.blocks[1].PreviousHash = "not-the-genesis-hash"

	idx, ok := bc.VerifyChain()
	if ok {
		t.Fatal("expected chain to be invalid with broken link")
	}
	if idx != 1 {
		t.Fatalf("expected first bad index=1, got %d", idx)
	}
}

func TestSearchTransactionsEmpty(t *testing.T) {
	bc := NewBlockchain()
	results := bc.SearchTransactions("alice", "")
	if len(results) != 0 {
		t.Fatalf("expected no results for empty chain, got %d", len(results))
	}
}

func TestSearchTransactionsBySender(t *testing.T) {
	bc := NewBlockchain()
	bc.AddBlock([]Transaction{{Sender: "alice", Recipient: "bob", Amount: 10}})
	bc.AddBlock([]Transaction{{Sender: "carol", Recipient: "alice", Amount: 5}})

	results := bc.SearchTransactions("alice", "")
	if len(results) != 1 {
		t.Fatalf("expected 1 block for sender=alice, got %d", len(results))
	}
	if results[0].Transactions[0].Sender != "alice" {
		t.Fatal("unexpected block returned")
	}
}

func TestSearchTransactionsByRecipient(t *testing.T) {
	bc := NewBlockchain()
	bc.AddBlock([]Transaction{{Sender: "alice", Recipient: "bob", Amount: 10}})
	bc.AddBlock([]Transaction{{Sender: "carol", Recipient: "bob", Amount: 5}})
	bc.AddBlock([]Transaction{{Sender: "alice", Recipient: "carol", Amount: 2}})

	results := bc.SearchTransactions("", "bob")
	if len(results) != 2 {
		t.Fatalf("expected 2 blocks for recipient=bob, got %d", len(results))
	}
}

func TestSearchTransactionsBothFilters(t *testing.T) {
	bc := NewBlockchain()
	bc.AddBlock([]Transaction{{Sender: "alice", Recipient: "bob", Amount: 1}})
	bc.AddBlock([]Transaction{{Sender: "alice", Recipient: "carol", Amount: 2}})

	results := bc.SearchTransactions("alice", "carol")
	if len(results) != 1 {
		t.Fatalf("expected 1 block for alice→carol, got %d", len(results))
	}
}
