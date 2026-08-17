package secretbox

import (
	"bytes"
	"testing"
)

func TestEncryptDecrypt(t *testing.T) {
	box, err := New(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	first, err := box.Encrypt("smtp.password", "secret-value")
	if err != nil || !IsEncrypted(first) || first == "secret-value" {
		t.Fatalf("unexpected encrypted value %q: %v", first, err)
	}
	second, _ := box.Encrypt("smtp.password", "secret-value")
	if first == second {
		t.Fatal("random nonce was not applied")
	}
	plaintext, err := box.Decrypt("smtp.password", first)
	if err != nil || plaintext != "secret-value" {
		t.Fatalf("decrypt=%q, %v", plaintext, err)
	}
}

func TestContextBindingAndPlaintextCompatibility(t *testing.T) {
	box, _ := New(bytes.Repeat([]byte{3}, 32))
	value, _ := box.Encrypt("oauth.github", "client-secret")
	if _, err := box.Decrypt("oauth.linuxdo", value); err == nil {
		t.Fatal("secret decrypted under a different context")
	}
	plaintext, err := box.Decrypt("legacy", "legacy-plaintext")
	if err != nil || plaintext != "legacy-plaintext" {
		t.Fatalf("legacy plaintext compatibility failed: %q, %v", plaintext, err)
	}
}
