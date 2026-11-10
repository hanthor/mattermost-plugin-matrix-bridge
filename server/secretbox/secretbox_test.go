package secretbox

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSealAndOpen(t *testing.T) {
	box := New("server-secret", "tokens")
	sealed, err := box.Seal("syt_abc")
	require.NoError(t, err)
	assert.True(t, IsSealed(sealed))
	assert.NotContains(t, sealed, "syt_abc")

	plain, legacy, err := box.Open(sealed)
	require.NoError(t, err)
	assert.False(t, legacy)
	assert.Equal(t, "syt_abc", plain)

	again, _ := box.Seal("syt_abc")
	assert.NotEqual(t, sealed, again, "every seal uses a fresh nonce")
}

func TestOpenRejectsWrongKeysAndPurposes(t *testing.T) {
	sealed, _ := New("a", "tokens").Seal("x")
	_, _, err := New("b", "tokens").Open(sealed)
	assert.Error(t, err)
	_, _, err = New("a", "pickle").Open(sealed)
	assert.Error(t, err, "keys are per purpose")
	_, _, err = (*Box)(nil).Open(sealed)
	assert.Error(t, err)
}

func TestLegacyPlainValuesPassThrough(t *testing.T) {
	plain, legacy, err := New("a", "tokens").Open("plain-token")
	require.NoError(t, err)
	assert.True(t, legacy)
	assert.Equal(t, "plain-token", plain)
}

func TestNoSecretMeansNoBox(t *testing.T) {
	assert.Nil(t, New("", "tokens"))
	out, err := (*Box)(nil).Seal("x")
	require.NoError(t, err)
	assert.Equal(t, "x", out)
}
