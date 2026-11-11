package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLocalpartOnThisServer(t *testing.T) {
	p := &Plugin{}
	p.setConfiguration(&configuration{MatrixServerDomain: "hs.test"})

	lp, ok := p.localpartOnThisServer("@ada:hs.test")
	assert.True(t, ok)
	assert.Equal(t, "ada", lp)

	_, ok = p.localpartOnThisServer("@ada:other.org")
	assert.False(t, ok, "accounts on other servers are never asked of MAS")
	_, ok = p.localpartOnThisServer("ada")
	assert.False(t, ok)

	p.setConfiguration(&configuration{})
	_, ok = p.localpartOnThisServer("@ada:hs.test")
	assert.False(t, ok)
}

func TestTokenFromMASNeedsConfiguration(t *testing.T) {
	p := &Plugin{}
	p.setConfiguration(&configuration{})
	_, err := p.tokenFromMAS("@ada:hs.test")
	assert.Error(t, err)
}
