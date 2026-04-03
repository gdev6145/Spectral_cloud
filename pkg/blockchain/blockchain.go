package blockchain

import (
    "crypto/sha256"
    "fmt"
    "sync"
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
    mu     sync.Mutex
}

// NewBlockchain initializes a new Blockchain.func NewBlockchain() *Blockchain {
    return &Blockchain{
        blocks: []*Block{createGenesisBlock()},
    }
}

// createGenesisBlock creates the first block in the blockchain
func createGenesisBlock() *Block {
    return &Block{
        Index:        0,
        Timestamp:    "2026-04-03 17:40:29",
        Transactions: nil,
        PreviousHash: "",
        Hash:         calculateHash(0, "", nil),
    }
}

// calculateHash generates a hash for the block
func calculateHash(index int, timestamp string, transactions []Transaction) string {
    record := fmt.Sprintf("%d%s%v", index, timestamp, transactions)
    h := sha256.New()
    h.Write([]byte(record))
    return fmt.Sprintf("%x", h.Sum(nil))
}

// AddBlock adds a new block to the blockchain
func (bc *Blockchain) AddBlock(transactions []Transaction) {
    bc.mu.Lock()
    defer bc.mu.Unlock()
    previousBlock := bc.blocks[len(bc.blocks)-1]
    newIndex := previousBlock.Index + 1
    newTimestamp := "2026-04-03 17:40:29" // Ideally this should be the current time
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