// Herramienta de publicación: crea la llave de firma y firma lo que se publica.
//
//	go run ./scripts/release keygen <archivo>    crea la llave privada e imprime la pública
//	go run ./scripts/release sign <dir> <exe>... escribe checksums.txt y <exe>.sig
//
// sign lee la llave de EL_BUSCADOR_SIGNING_KEY. La llave privada nunca entra al repo:
// quien la tenga puede publicar actualizaciones que todas las copias de la app aceptan.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	switch {
	case len(args) == 2 && args[0] == "keygen":
		return keygen(args[1])
	case len(args) >= 3 && args[0] == "sign":
		return sign(os.Getenv("EL_BUSCADOR_SIGNING_KEY"), args[1], args[2:])
	}
	return errors.New("uso: keygen <archivo> | sign <dir> <exe>...")
}

func keygen(path string) error {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	// O_EXCL: sobrescribir la llave dejaría sin actualizaciones a quien ya instaló.
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := file.WriteString(hex.EncodeToString(private.Seed()) + "\n"); err != nil {
		return err
	}
	fmt.Println(hex.EncodeToString(public))
	return nil
}

// sign firma el SHA-256 de cada archivo, que es lo que el updater verifica.
func sign(key, dir string, names []string) error {
	seed, err := hex.DecodeString(strings.TrimSpace(key))
	if err != nil || len(seed) != ed25519.SeedSize {
		return errors.New("EL_BUSCADOR_SIGNING_KEY no es una llave válida")
	}
	private := ed25519.NewKeyFromSeed(seed)

	var checksums strings.Builder
	for _, name := range names {
		digest, err := sha256File(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		fmt.Fprintf(&checksums, "%s  %s\n", hex.EncodeToString(digest), name)
		if err := os.WriteFile(filepath.Join(dir, name+".sig"), ed25519.Sign(private, digest), 0o644); err != nil {
			return err
		}
	}
	return os.WriteFile(filepath.Join(dir, "checksums.txt"), []byte(checksums.String()), 0o644)
}

func sha256File(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return nil, err
	}
	return hash.Sum(nil), nil
}
