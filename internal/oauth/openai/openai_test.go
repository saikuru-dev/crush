package openai

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExtractAccountID(t *testing.T) {
	t.Parallel()

	token := jwtForClaims(t, map[string]any{
		"chatgpt_account_id": "acct_123",
	})
	require.Equal(t, "acct_123", extractAccountID(token))
}

func TestTokenFromResponseUsesClaimsAndPreservesRefreshToken(t *testing.T) {
	t.Parallel()

	claimsToken := jwtForClaims(t, map[string]any{
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": "acct_456",
		},
	})

	tok := tokenFromResponse(tokenResponse{
		AccessToken:  claimsToken,
		RefreshToken: "refresh-abc",
		ExpiresIn:    3600,
	})

	require.Equal(t, "refresh-abc", tok.RefreshToken)
	require.Equal(t, "acct_456", tok.AccountID)
	require.NotZero(t, tok.ExpiresAt)
}

func jwtForClaims(t *testing.T, claims map[string]any) string {
	t.Helper()

	head := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	body, err := json.Marshal(claims)
	require.NoError(t, err)
	pay := base64.RawURLEncoding.EncodeToString(body)
	return head + "." + pay + ".sig"
}
