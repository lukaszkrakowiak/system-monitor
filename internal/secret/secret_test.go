package secret

import (
	"errors"
	"strings"
	"testing"
)

const key = "0123456789abcdef0123456789abcdef"

func TestRoundTripAndRandomNonce(t *testing.T) {
	b, err := New(key)
	if err != nil {
		t.Fatal(err)
	}
	e1, _ := b.Encrypt("p@ss wörd")
	e2, _ := b.Encrypt("p@ss wörd")
	if e1 == e2 || strings.Contains(e1, "p@ss") {
		t.Fatalf("ciphertext must be randomized and opaque: %q %q", e1, e2)
	}
	for _, e := range []string{e1, e2} {
		if got, err := b.Decrypt(e); err != nil || got != "p@ss wörd" {
			t.Fatalf("decrypt: %q %v", got, err)
		}
	}
}

func TestEmptySecretAndKeyHandling(t *testing.T) {
	b, _ := New(key)
	if e, err := b.Encrypt(""); e != "" || err != nil {
		t.Fatalf("empty: %q %v", e, err)
	}
	if p, err := b.Decrypt(""); p != "" || err != nil {
		t.Fatalf("empty decrypt: %q %v", p, err)
	}
	if _, err := New("short"); err == nil {
		t.Fatal("short key must be rejected")
	}
	var none *Box
	if _, err := none.Encrypt("x"); !errors.Is(err, ErrNoKey) {
		t.Fatalf("nil box encrypt: %v", err)
	}
	if _, err := none.Decrypt("abc"); !errors.Is(err, ErrNoKey) {
		t.Fatalf("nil box decrypt: %v", err)
	}
	if e, err := none.Encrypt(""); e != "" || err != nil { // brak sekretu nie wymaga klucza
		t.Fatalf("nil box, empty secret: %q %v", e, err)
	}
}

func TestTamperAndWrongKey(t *testing.T) {
	b, _ := New(key)
	e, _ := b.Encrypt("secret")
	other, _ := New(strings.Repeat("z", 40))
	if _, err := other.Decrypt(e); err == nil {
		t.Fatal("wrong key must fail")
	}
	if _, err := b.Decrypt(e[:len(e)-4] + "AAAA"); err == nil {
		t.Fatal("tampered ciphertext must fail")
	}
	if _, err := b.Decrypt("not base64!!"); err == nil {
		t.Fatal("garbage must fail")
	}
	if _, err := b.Decrypt("QQ=="); err == nil {
		t.Fatal("too short must fail")
	}
}
