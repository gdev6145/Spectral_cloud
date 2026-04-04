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
