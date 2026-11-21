package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSignProducesWhatTheUpdaterVerifies(t *testing.T) {
	dir := t.TempDir()
	content := []byte("programa")
	if err := os.WriteFile(filepath.Join(dir, "app.exe"), content, 0o644); err != nil {
		t.Fatal(err)
	}
	public, private, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := sign(hex.EncodeToString(private.Seed()), dir, []string{"app.exe"}); err != nil {
		t.Fatal(err)
	}

	digest := sha256.Sum256(content)
	signature, err := os.ReadFile(filepath.Join(dir, "app.exe.sig"))
	if err != nil {
		t.Fatal(err)
	}
	if !ed25519.Verify(public, digest[:], signature) {
		t.Fatal("la firma no corresponde al SHA-256 del archivo")
	}
	checksums, err := os.ReadFile(filepath.Join(dir, "checksums.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(checksums), hex.EncodeToString(digest[:])+"  app.exe") {
		t.Fatalf("checksums: %s", checksums)
	}
}

func TestSignRejectsBadKey(t *testing.T) {
	for _, key := range []string{"", "zz", "abcd"} {
		if err := sign(key, t.TempDir(), []string{"x"}); err == nil {
			t.Errorf("aceptó la llave %q", key)
		}
	}
}

func TestKeygenNeverOverwrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "llave.txt")
	if err := keygen(path); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := keygen(path); err == nil {
		t.Fatal("sobrescribió una llave existente")
	}
	second, _ := os.ReadFile(path)
	if string(first) != string(second) {
		t.Fatal("cambió la llave")
	}
}
