package main

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"time"
)

// Appservice transactions are retried by the homeserver until acknowledged, so
// each one must be handled once. The record of handled transactions is kept
// in the key-value store, not in memory, so a retry that reaches another node
// of a cluster (or the same node after a restart) is still recognised. A small
// in-memory set in front of it saves the lookup for the common case.
//
// The check and the record are two steps, so two nodes receiving the same
// transaction at the same instant could both handle it; the homeserver sends
// one transaction at a time per appservice, which makes that a theoretical
// case, and handling is idempotent for most events anyway.

const (
	txnKeyPrefix     = "txn_"
	txnRetention     = time.Hour
	txnCleanupBatch  = 500
	txnMaxKeyIDBytes = 100
)

func transactionKey(txnID string) string {
	if len(txnID) > txnMaxKeyIDBytes {
		sum := sha256.Sum256([]byte(txnID))
		return txnKeyPrefix + "h_" + hex.EncodeToString(sum[:])
	}
	return txnKeyPrefix + txnID
}

// transactionSeen reports whether a transaction was already handled.
func (p *Plugin) transactionSeen(txnID string) bool {
	if _, ok := p.recentTransactions.Load(txnID); ok {
		return true
	}
	if p.kvstore == nil {
		return false
	}
	data, err := p.kvstore.Get(transactionKey(txnID))
	if err != nil || len(data) == 0 {
		return false
	}
	p.recentTransactions.Store(txnID, time.Now())
	return true
}

// markTransaction records a transaction as handled. It returns true every
// hundredth call, which is when old records should be cleaned up.
func (p *Plugin) markTransaction(txnID string) (cleanupDue bool) {
	now := time.Now()
	p.recentTransactions.Store(txnID, now)
	if p.kvstore != nil {
		if err := p.kvstore.Set(transactionKey(txnID), []byte(strconv.FormatInt(now.Unix(), 10))); err != nil {
			p.logger.LogWarn("Could not record a handled transaction", "error", err, "txn_id", txnID)
		}
	}
	n := 0
	p.recentTransactions.Range(func(_, _ any) bool { n++; return true })
	return n%100 == 0
}

// cleanupOldTransactions forgets transactions older than an hour, in memory
// and in the key-value store.
func (p *Plugin) cleanupOldTransactions() {
	cutoff := time.Now().Add(-txnRetention)
	p.recentTransactions.Range(func(k, v any) bool {
		if t, ok := v.(time.Time); ok && t.Before(cutoff) {
			p.recentTransactions.Delete(k)
		}
		return true
	})
	if p.kvstore == nil {
		return
	}
	deleted := 0
	for page := 0; deleted < txnCleanupBatch; page++ {
		keys, err := p.kvstore.ListKeysWithPrefix(page, 200, txnKeyPrefix)
		if err != nil || len(keys) == 0 {
			return
		}
		for _, key := range keys {
			data, err := p.kvstore.Get(key)
			if err != nil {
				continue
			}
			secs, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
			if err == nil && time.Unix(secs, 0).After(cutoff) {
				continue
			}
			if p.kvstore.Delete(key) == nil {
				deleted++
			}
		}
		if len(keys) < 200 {
			return
		}
	}
}
