// Package secret szyfruje sekrety zapisywane w SQLite (np. hasło SMTP) algorytmem AES-256-GCM.
// Klucz pochodzi z pliku env (SM_SECRET_KEY), nigdy z bazy — sama kopia bazy nie ujawnia sekretów.
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
)

// MinKeyLen to minimalna długość SM_SECRET_KEY (zalecane: openssl rand -base64 32).
const MinKeyLen = 32

// ErrNoKey oznacza brak skonfigurowanego klucza — sekretów nie wolno wtedy zapisywać ani odczytywać.
var ErrNoKey = errors.New("secret storage is not configured (set SM_SECRET_KEY)")

// Box szyfruje i deszyfruje sekrety. Zero wartości (nil) zwraca ErrNoKey.
type Box struct{ aead cipher.AEAD }

// New buduje Box z klucza tekstowego; z niego liczony jest 256-bitowy klucz (SHA-256).
func New(key string) (*Box, error) {
	if len(key) < MinKeyLen {
		return nil, fmt.Errorf("secret key must have at least %d characters", MinKeyLen)
	}
	sum := sha256.Sum256([]byte(key))
	block, err := aes.NewCipher(sum[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

// Encrypt zwraca base64(nonce||szyfrogram). Pusty sekret to pusty wynik (brak sekretu).
func (b *Box) Encrypt(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	if b == nil {
		return "", ErrNoKey
	}
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(b.aead.Seal(nonce, nonce, []byte(plain), nil)), nil
}

// Decrypt odwraca Encrypt. Pusty wejściowy tekst to pusty sekret. Zmieniony szyfrogram lub zły klucz -> błąd.
func (b *Box) Decrypt(enc string) (string, error) {
	if enc == "" {
		return "", nil
	}
	if b == nil {
		return "", ErrNoKey
	}
	raw, err := base64.StdEncoding.DecodeString(enc)
	if err != nil || len(raw) < b.aead.NonceSize() {
		return "", errors.New("secret: malformed ciphertext")
	}
	n := b.aead.NonceSize()
	plain, err := b.aead.Open(nil, raw[:n], raw[n:], nil)
	if err != nil {
		return "", errors.New("secret: cannot decrypt (wrong SM_SECRET_KEY or corrupted data)")
	}
	return string(plain), nil
}
