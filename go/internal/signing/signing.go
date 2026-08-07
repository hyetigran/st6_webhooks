// Package signing computes the receiver-contract's HMAC-SHA256 signature.
// Mirrors node/src/lib/signing.ts.
package signing

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// SignPayload signs with every still-active secret (current, plus secondary
// during a rotation overlap window), not just the current one — docs/adr/0003
// — so a receiver on either secret verifies successfully throughout the
// overlap, closing the bootstrapping gap a receiver-only dual-check has.
func SignPayload(secrets []string, timestamp int64, rawBody string) string {
	signedString := fmt.Sprintf("%d.%s", timestamp, rawBody)
	signatures := make([]string, len(secrets))
	for i, secret := range secrets {
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte(signedString))
		signatures[i] = hex.EncodeToString(mac.Sum(nil))
	}
	return strings.Join(signatures, ",")
}
