// Package crypto handles the two credential-protection mechanisms this
// service needs: a fast hash for high-entropy API keys, and reversible
// encryption for signing secrets (which must stay recoverable for HMAC).
// Mirrors node/src/lib/crypto.ts.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
)

// HashAPIKey hashes with a fast cryptographic hash (SHA-256), not a slow
// password hash (bcrypt/scrypt/argon2). Those exist to slow down
// brute-forcing a low-entropy, human-chosen secret — an API key is a
// high-entropy random token no one is guessing, and a slow hash would just
// add needless latency to every authenticated request for no security
// benefit.
func HashAPIKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// EncryptSecret/DecryptSecret: signing secrets must be recoverable (HMAC
// needs the raw value), so they can't be hashed like the API key —
// encrypted at rest instead, so a DB dump alone doesn't hand over every
// customer's signing secret in plaintext. AES-256-GCM; output is
// iv || authTag || ciphertext, base64-encoded.
func EncryptSecret(plaintext string, key []byte) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("crypto: new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("crypto: new gcm: %w", err)
	}

	iv := make([]byte, 12)
	if _, err := rand.Read(iv); err != nil {
		return "", fmt.Errorf("crypto: generate iv: %w", err)
	}

	// Go's GCM Seal appends ciphertext||authTag, but the Node side lays out
	// iv||authTag||ciphertext — reorder to match so either stack can decrypt
	// what the other encrypted.
	sealed := gcm.Seal(nil, iv, []byte(plaintext), nil)
	ciphertext := sealed[:len(sealed)-gcm.Overhead()]
	authTag := sealed[len(sealed)-gcm.Overhead():]

	out := make([]byte, 0, len(iv)+len(authTag)+len(ciphertext))
	out = append(out, iv...)
	out = append(out, authTag...)
	out = append(out, ciphertext...)
	return base64.StdEncoding.EncodeToString(out), nil
}

func DecryptSecret(stored string, key []byte) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(stored)
	if err != nil {
		return "", fmt.Errorf("crypto: decode base64: %w", err)
	}
	if len(raw) < 12+16 {
		return "", fmt.Errorf("crypto: stored value too short")
	}
	iv := raw[:12]
	authTag := raw[12:28]
	ciphertext := raw[28:]

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("crypto: new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("crypto: new gcm: %w", err)
	}

	sealed := make([]byte, 0, len(ciphertext)+len(authTag))
	sealed = append(sealed, ciphertext...)
	sealed = append(sealed, authTag...)
	plaintext, err := gcm.Open(nil, iv, sealed, nil)
	if err != nil {
		return "", fmt.Errorf("crypto: decrypt: %w", err)
	}
	return string(plaintext), nil
}
