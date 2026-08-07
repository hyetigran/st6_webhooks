package crypto_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"webhooks-go/internal/crypto"
)

func TestEncryptDecryptSecretRoundTrip(t *testing.T) {
	key := []byte("abcdefghijklmnopqrstuvwxyz012345")
	plaintext := "whsec_test_roundtrip_value"

	encrypted, err := crypto.EncryptSecret(plaintext, key)
	require.NoError(t, err)
	require.NotEqual(t, plaintext, encrypted)

	decrypted, err := crypto.DecryptSecret(encrypted, key)
	require.NoError(t, err)
	require.Equal(t, plaintext, decrypted)
}

func TestDecryptSecretRejectsWrongKey(t *testing.T) {
	key := []byte("abcdefghijklmnopqrstuvwxyz012345")
	wrongKey := []byte("00000000000000000000000000000000"[:32])

	encrypted, err := crypto.EncryptSecret("whsec_value", key)
	require.NoError(t, err)

	_, err = crypto.DecryptSecret(encrypted, wrongKey)
	require.Error(t, err)
}

func TestHashAPIKeyIsDeterministicAndDistinguishesInput(t *testing.T) {
	require.Equal(t, crypto.HashAPIKey("tenant_abc"), crypto.HashAPIKey("tenant_abc"))
	require.NotEqual(t, crypto.HashAPIKey("tenant_abc"), crypto.HashAPIKey("tenant_xyz"))
}
