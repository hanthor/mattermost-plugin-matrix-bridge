package main

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func resetRecentTransactions() {
	recentTransactions.Range(func(k, _ any) bool { recentTransactions.Delete(k); return true })
}

func TestATransactionHandledByAnotherNodeIsRecognised(t *testing.T) {
	resetRecentTransactions()
	hs := newFakeSynapse(t)
	nodeA, _ := newMirrorTestBridge(t, hs)
	nodeB, _ := newMirrorTestBridge(t, hs)
	nodeB.kvstore = nodeA.kvstore // the shared key-value store of a cluster

	assert.False(t, nodeA.transactionSeen("txn-1"))
	nodeA.markTransaction("txn-1")
	resetRecentTransactions() // node B has no memory of it

	assert.True(t, nodeB.transactionSeen("txn-1"), "the record is in the shared store, not in node A's memory")
	assert.False(t, nodeB.transactionSeen("txn-2"))
}

func TestLongTransactionIDsStayWithinTheKeyLimit(t *testing.T) {
	long := strings.Repeat("x", 400)
	key := transactionKey(long)
	assert.LessOrEqual(t, len(key), 150)
	assert.NotEqual(t, transactionKey(long), transactionKey(long+"y"))
	assert.Equal(t, "txn_abc", transactionKey("abc"))
}

func TestOldTransactionRecordsAreCleanedUp(t *testing.T) {
	resetRecentTransactions()
	hs := newFakeSynapse(t)
	p, _ := newMirrorTestBridge(t, hs)

	require.NoError(t, p.kvstore.Set(transactionKey("old"), []byte("1")))
	p.markTransaction("fresh")
	recentTransactions.Store("old", time.Now().Add(-2*time.Hour))

	p.cleanupOldTransactions()

	old, _ := p.kvstore.Get(transactionKey("old"))
	fresh, _ := p.kvstore.Get(transactionKey("fresh"))
	assert.Empty(t, old, "an hour-old record is removed")
	assert.NotEmpty(t, fresh, "a recent one is kept")
	assert.False(t, p.transactionSeen("old"))
	assert.True(t, p.transactionSeen("fresh"))
}
