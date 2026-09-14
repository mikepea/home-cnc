// Package sign manages the server's ed25519 signing key and provides signing.
//
// The private key is the trust anchor for commands. In v1 it lives on the
// server (server-trusted model); agents verify against the matching public key,
// which is baked into their config. Because agents already verify a signature,
// moving the private key into the controller's browser later (issue #2) is a
// key-location change, not an agent redesign.
package sign

import (
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
)

// Signer wraps the server's key pair.
type Signer struct {
	priv ed25519.PrivateKey
	Pub  ed25519.PublicKey
}

// Sign returns a base64(std) ed25519 signature over msg.
func (s *Signer) Sign(msg []byte) string {
	return base64.StdEncoding.EncodeToString(ed25519.Sign(s.priv, msg))
}

// PubBase64 returns the public key as base64(std), suitable for pasting into an
// agent config file.
func (s *Signer) PubBase64() string {
	return base64.StdEncoding.EncodeToString(s.Pub)
}

// LoadOrCreate loads the signing key from dir, creating a fresh one on first
// run. The private seed is written 0600; the public key is written alongside it
// for convenience.
func LoadOrCreate(dir string) (*Signer, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	keyPath := filepath.Join(dir, "signing.key")
	pubPath := filepath.Join(dir, "signing.pub")

	if b, err := os.ReadFile(keyPath); err == nil {
		seed, err := base64.StdEncoding.DecodeString(string(b))
		if err != nil {
			return nil, fmt.Errorf("decode signing.key: %w", err)
		}
		if len(seed) != ed25519.SeedSize {
			return nil, fmt.Errorf("signing.key has wrong size %d", len(seed))
		}
		priv := ed25519.NewKeyFromSeed(seed)
		return &Signer{priv: priv, Pub: priv.Public().(ed25519.PublicKey)}, nil
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		return nil, err
	}
	seed := priv.Seed()
	if err := os.WriteFile(keyPath, []byte(base64.StdEncoding.EncodeToString(seed)), 0o600); err != nil {
		return nil, err
	}
	if err := os.WriteFile(pubPath, []byte(base64.StdEncoding.EncodeToString(pub)), 0o644); err != nil {
		return nil, err
	}
	return &Signer{priv: priv, Pub: pub}, nil
}
