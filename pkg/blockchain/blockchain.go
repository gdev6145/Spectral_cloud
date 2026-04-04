package blockchain

import (
	"crypto/sha256"
	"fmt"
	"sync"
	"time"
)

// Block represents a single block in the blockchain
type Block struct {
	Index        int
	Timestamp    string
	Transactions []Transaction
	PreviousHash string
	Hash         string
}

// Transaction represents a transaction in the blockchain
type Transaction struct {
	Sender    string
	Recipient string
	Amount    float64
}

// Blockchain represents the complete chain
type Blockchain struct {
	blocks []*Block
	mu     sync.RWMutex
}

// NewBlockchain initializes a new Blockchain.
func NewBlockchain() *Blockchain {
	return &Blockchain{
		blocks: []*Block{createGenesisBlock()},
	}
}

// createGenesisBlock creates the first block in the blockchain
func createGenesisBlock() *Block {
	ts := time.Now().UTC().Format(time.RFC3339)
	return &Block{
		Index:        0,
		Timestamp:    ts,
		Transactions: nil,
		PreviousHash: "",
		Hash:         calculateHash(0, ts, nil),
	}
}

// calculateHash generates a hash for the block
func calculateHash(index int, timestamp string, transactions []Transaction) string {
	record := fmt.Sprintf("%d%s%v", index, timestamp, transactions)
	h := sha256.New()
	h.Write([]byte(record))
	return fmt.Sprintf("%x", h.Sum(nil))
}

// Verify checks whether a block's hash matches its contents.
func Verify(block *Block) bool {
	if block == nil {
		return false
	}
	expected := calculateHash(block.Index, block.Timestamp, block.Transactions)
	return block.Hash == expected
}

// AddBlock adds a new block to the blockchain
func (bc *Blockchain) AddBlock(transactions []Transaction) {
	bc.mu.Lock()
	defer bc.mu.Unlock()
	previousBlock := bc.blocks[len(bc.blocks)-1]
	newIndex := previousBlock.Index + 1
	newTimestamp := time.Now().UTC().Format(time.RFC3339)
	newHash := calculateHash(newIndex, newTimestamp, transactions)
	newBlock := &Block{
		Index:        newIndex,
		Timestamp:    newTimestamp,
		Transactions: transactions,
		PreviousHash: previousBlock.Hash,
		Hash:         newHash,
	}
	bc.blocks = append(bc.blocks, newBlock)
}

// Height returns the current height of the chain.
func (bc *Blockchain) Height() int {
	bc.mu.RLock()
	defer bc.mu.RUnlock()
	return len(bc.blocks)
}

// LastBlock returns the most recent block.
func (bc *Blockchain) LastBlock() *Block {
	bc.mu.RLock()
	defer bc.mu.RUnlock()
	if len(bc.blocks) == 0 {
		return nil
	}
	return bc.blocks[len(bc.blocks)-1]
}

// Snapshot returns a copy of the blocks for persistence or inspection.
func (bc *Blockchain) Snapshot() []Block {
	bc.mu.RLock()
	defer bc.mu.RUnlock()
	out := make([]Block, len(bc.blocks))
	for i, b := range bc.blocks {
		out[i] = *b
	}
	return out
}

// Load replaces the chain with the provided blocks.
func (bc *Blockchain) Load(blocks []Block) {
	bc.mu.Lock()
	defer bc.mu.Unlock()
	if len(blocks) == 0 {
		bc.blocks = []*Block{createGenesisBlock()}
		return
	}
	out := make([]*Block, len(blocks))
	for i := range blocks {
		b := blocks[i]
		out[i] = &b
	}
	bc.blocks = out
}
