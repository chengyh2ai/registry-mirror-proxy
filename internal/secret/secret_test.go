package secret

import "testing"

func TestEncryptDecrypt(t *testing.T) {
	encrypted, err := Encrypt("hello", "test-key")
	if err != nil {
		t.Fatal(err)
	}
	if !IsEncrypted(encrypted) {
		t.Fatalf("encrypted value does not have prefix: %q", encrypted)
	}
	plain, err := Decrypt(encrypted, "test-key")
	if err != nil {
		t.Fatal(err)
	}
	if plain != "hello" {
		t.Fatalf("plain = %q", plain)
	}
}

func TestDecryptPlainValue(t *testing.T) {
	plain, err := Decrypt("hello", "")
	if err != nil {
		t.Fatal(err)
	}
	if plain != "hello" {
		t.Fatalf("plain = %q", plain)
	}
}
