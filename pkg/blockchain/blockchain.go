package blockchain

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

// Block represents a single block in the blockchain
type Block struct {
	Index        int           `json:"index"`
	Timestamp    string        `json:"timestamp"`
	Transactions []Transaction `json:"transactions"`
	PreviousHash string        `json:"previous_hash"`
	Hash         string        `json:"hash"`
	// Signature is an optional HMAC-SHA256 of the block Hash using a signing
	// key. It provides verifiable authorship without changing the Hash itself.
	Signature    string        `json:"signature,omitempty"`
}

// Transaction represents a transaction in the blockchain
type Transaction struct {
	Sender    string  `json:"sender"`
	Recipient string  `json:"recipient"`
	Amount    float64 `json:"amount"`
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

// NewBlockchainFromBlocks creates a blockchain from a predefined set of blocks.
func NewBlockchainFromBlocks(blocks []*Block) *Blockchain {
	if len(blocks) == 0 {
		return NewBlockchain()
	}
	return &Blockchain{blocks: blocks}
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

// SignBlock computes an HMAC-SHA256 over the block's Hash using signingKey and
// stores it in block.Signature. It returns the signature hex string.
func SignBlock(block *Block, signingKey string) string {
	if block == nil || signingKey == "" {
		return ""
	}
	mac := hmac.New(sha256.New, []byte(signingKey))
	mac.Write([]byte(block.Hash))
	sig := hex.EncodeToString(mac.Sum(nil))
	block.Signature = sig
	return sig
}

// VerifySignature checks block.Signature against the expected HMAC-SHA256 of
// block.Hash using the provided key. Returns false if block is nil or unsigned.
func VerifySignature(block *Block, signingKey string) bool {
	if block == nil || block.Signature == "" || signingKey == "" {
		return false
	}
	mac := hmac.New(sha256.New, []byte(signingKey))
	mac.Write([]byte(block.Hash))
	expected := hex.EncodeToString(mac.Sum(nil))
	if len(block.Signature) != len(expected) {
		return false
	}
	var diff byte
	for i := 0; i < len(block.Signature); i++ {
		diff |= block.Signature[i] ^ expected[i]
	}
	return diff == 0
}

// AddBlock adds a new block to the blockchain
func (bc *Blockchain) AddBlock(transactions []Transaction) *Block {
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
	return newBlock
}

// AddSignedBlock adds a new block and signs it with signingKey. If signingKey
// is empty the block is added without a signature (same as AddBlock).
func (bc *Blockchain) AddSignedBlock(transactions []Transaction, signingKey string) *Block {
	block := bc.AddBlock(transactions)
	if signingKey != "" {
		SignBlock(block, signingKey)
	}
	return block
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

// Blocks returns a copy of all blocks.
func (bc *Blockchain) Blocks() []*Block {
	bc.mu.RLock()
	defer bc.mu.RUnlock()
	out := make([]*Block, len(bc.blocks))
	copy(out, bc.blocks)
	return out
}

// RemoveLastBlock removes the most recent block, preserving the genesis block.
func (bc *Blockchain) RemoveLastBlock() {
	bc.mu.Lock()
	defer bc.mu.Unlock()
	if len(bc.blocks) <= 1 {
		return
	}
	bc.blocks = bc.blocks[:len(bc.blocks)-1]
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

// GetBlock returns the block at the given index. The second return value is
// false when the index is out of range.
func (bc *Blockchain) GetBlock(index int) (Block, bool) {
	bc.mu.RLock()
	defer bc.mu.RUnlock()
	if index < 0 || index >= len(bc.blocks) {
		return Block{}, false
	}
	return *bc.blocks[index], true
}

// BlockRange returns blocks in the half-open range [start, end). Indices are
// clamped to valid bounds; a zero-length range returns nil.
func (bc *Blockchain) BlockRange(start, end int) []Block {
	bc.mu.RLock()
	defer bc.mu.RUnlock()
	n := len(bc.blocks)
	if start < 0 {
		start = 0
	}
	if end > n {
		end = n
	}
	if start >= end {
		return nil
	}
	out := make([]Block, end-start)
	for i, b := range bc.blocks[start:end] {
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
