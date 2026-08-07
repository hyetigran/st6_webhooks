package signing_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"webhooks-go/internal/signing"
)

func expectedSignature(secret string, timestamp int64, rawBody string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(fmt.Sprintf("%d.%s", timestamp, rawBody)))
	return hex.EncodeToString(mac.Sum(nil))
}

func TestSignPayloadSingleSecret(t *testing.T) {
	sig := signing.SignPayload([]string{"whsec_abc123"}, 1_700_000_000, `{"orderId":"1"}`)
	require.Equal(t, expectedSignature("whsec_abc123", 1_700_000_000, `{"orderId":"1"}`), sig)
}

func TestSignPayloadMultipleSecretsCommaJoined(t *testing.T) {
	sig := signing.SignPayload([]string{"whsec_new", "whsec_old"}, 1_700_000_000, `{"orderId":"1"}`)
	expected := strings.Join([]string{
		expectedSignature("whsec_new", 1_700_000_000, `{"orderId":"1"}`),
		expectedSignature("whsec_old", 1_700_000_000, `{"orderId":"1"}`),
	}, ",")
	require.Equal(t, expected, sig)
}
