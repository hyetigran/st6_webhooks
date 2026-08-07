// Package secrets generates high-entropy tokens for tenant API keys and
// endpoint signing secrets. Mirrors node/src/lib/secrets.ts.
package secrets

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// Generate returns a high-entropy "<prefix>_<48 hex chars>" token — used for
// both tenant API keys and endpoint signing secrets.
func Generate(prefix string) string {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		panic(fmt.Sprintf("secrets: failed to read random bytes: %v", err))
	}
	return fmt.Sprintf("%s_%s", prefix, hex.EncodeToString(buf))
}
