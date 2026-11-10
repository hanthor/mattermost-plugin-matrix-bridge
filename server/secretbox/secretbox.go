// Package secretbox seals small secrets (access tokens, key material) before
// they are written to the plugin key-value store, using a key that is derived
// from something held outside that store.
package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
)

// Prefix marks a sealed value. Anything without it is a legacy plain value.
const Prefix = "enc1:"

// Box seals and opens values with AES-256-GCM.
type Box struct {
	aead cipher.AEAD
}

// New derives a key for the given purpose from secret, which must not live
// in the store being protected. It returns nil when secret is empty.
func New(secret, purpose string) *Box {
	if secret == "" {
		return nil
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("mattermost-matrix-bridge/" + purpose))
	block, err := aes.NewCipher(mac.Sum(nil))
	if err != nil {
		return nil
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil
	}
	return &Box{aead: aead}
}

// IsSealed reports whether s looks like a sealed value.
func IsSealed(s string) bool { return strings.HasPrefix(s, Prefix) }

// Seal encrypts plain. A nil Box returns the value unchanged.
func (b *Box) Seal(plain string) (string, error) {
	if b == nil {
		return plain, nil
	}
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	out := b.aead.Seal(nonce, nonce, []byte(plain), nil)
	return Prefix + base64.RawStdEncoding.EncodeToString(out), nil
}

// Open decrypts a sealed value. A value without the prefix is returned as is,
// with legacy true, so callers can upgrade it.
func (b *Box) Open(s string) (plain string, legacy bool, err error) {
	if !IsSealed(s) {
		return s, true, nil
	}
	if b == nil {
		return "", false, errors.New("the value is sealed but no key is available")
	}
	raw, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(s, Prefix))
	if err != nil || len(raw) < b.aead.NonceSize() {
		return "", false, errors.New("the sealed value is malformed")
	}
	out, err := b.aead.Open(nil, raw[:b.aead.NonceSize()], raw[b.aead.NonceSize():], nil)
	if err != nil {
		return "", false, errors.New("the sealed value cannot be opened with the current key")
	}
	return string(out), false, nil
}
