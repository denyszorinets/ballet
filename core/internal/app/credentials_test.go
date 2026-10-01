package app_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/core/internal/domain/credential"
	"github.com/denyszorinets/ballet/core/internal/infra/secrets"
	"github.com/denyszorinets/ballet/core/internal/infra/store"
)

func newCredentials(t *testing.T) (*app.Credentials, rbacEnv) {
	t.Helper()
	env := newRBACEnv(t)
	seed(t, env)
	box, err := secrets.LoadKey(filepath.Join(t.TempDir(), "secrets.key"))
	require.NoError(t, err)
	return &app.Credentials{Store: env.store, Tenancy: env.store, Authz: env.rbac, Box: box, Now: time.Now, NewID: store.NewID}, env
}

func TestCredentials_ProjectOverrideWinsOverCustomerDefault(t *testing.T) {
	cr, _ := newCredentials(t)
	dave := user(t, "dave", "acme-admins")

	_, err := cr.Set(dave, app.SetCredentialInput{CustomerKey: "acme", Provider: credential.ProviderAnthropic, APIKey: "sk-default-1111"})
	require.NoError(t, err)
	_, err = cr.Set(dave, app.SetCredentialInput{ProjectKey: "WEB", Provider: credential.ProviderAnthropic, APIKey: "sk-web-2222", BaseURL: "https://llm.example"})
	require.NoError(t, err)

	got, err := cr.Resolve(t.Context(), "acme", "WEB", credential.ProviderAnthropic)
	require.NoError(t, err)
	assert.Equal(t, "sk-web-2222", got.APIKey)
	assert.Equal(t, "https://llm.example", got.BaseURL)

	got, err = cr.Resolve(t.Context(), "acme", "APP", credential.ProviderAnthropic)
	require.NoError(t, err)
	assert.Equal(t, "sk-default-1111", got.APIKey, "projects without override use the customer default")

	_, err = cr.Resolve(t.Context(), "acme", "WEB", credential.ProviderOpenAI)
	assert.ErrorIs(t, err, app.ErrNotFound)
	_, err = cr.Resolve(t.Context(), "acme", "GLX", credential.ProviderAnthropic)
	assert.ErrorIs(t, err, app.ErrNotFound, "project of another customer")
}

func TestCredentials_SecretsAreNeverListedAndEncryptedAtRest(t *testing.T) {
	cr, env := newCredentials(t)
	dave := user(t, "dave", "acme-admins")
	_, err := cr.Set(dave, app.SetCredentialInput{CustomerKey: "acme", Provider: credential.ProviderAnthropic, APIKey: "sk-very-secret-9876"})
	require.NoError(t, err)

	list, err := cr.List(dave, "acme")
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Empty(t, list[0].APIKey)
	assert.Equal(t, credential.Fingerprint("sk-very-secret-9876"), list[0].Fingerprint)
	assert.Contains(t, list[0].Fingerprint, "9876")

	var raw string
	require.NoError(t, env.store.DB().QueryRow(t.Context(), "SELECT ciphertext FROM credentials").Scan(&raw))
	assert.NotContains(t, raw, "secret")
	events, err := env.store.ListEvents(t.Context(), store.EventFilter{EntityType: "credential"})
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.NotContains(t, string(events[0].Payload), "secret")
}

func TestCredentials_ReplaceAndDelete(t *testing.T) {
	cr, _ := newCredentials(t)
	dave := user(t, "dave", "acme-admins")
	for _, key := range []string{"sk-old", "sk-new"} {
		_, err := cr.Set(dave, app.SetCredentialInput{ProjectKey: "WEB", Provider: credential.ProviderAnthropic, APIKey: key})
		require.NoError(t, err)
	}
	got, err := cr.Resolve(t.Context(), "acme", "WEB", credential.ProviderAnthropic)
	require.NoError(t, err)
	assert.Equal(t, "sk-new", got.APIKey)

	require.NoError(t, cr.Delete(dave, "", "WEB", credential.ProviderAnthropic))
	_, err = cr.Resolve(t.Context(), "acme", "WEB", credential.ProviderAnthropic)
	assert.ErrorIs(t, err, app.ErrNotFound)
	assert.ErrorIs(t, cr.Delete(dave, "", "WEB", credential.ProviderAnthropic), app.ErrNotFound)
}

func TestCredentials_AuthorizationAndValidation(t *testing.T) {
	cr, _ := newCredentials(t)
	bob := user(t, "bob", "acme-devs")
	dave := user(t, "dave", "acme-admins")

	_, err := cr.Set(bob, app.SetCredentialInput{CustomerKey: "acme", Provider: credential.ProviderAnthropic, APIKey: "k"})
	assert.ErrorIs(t, err, app.ErrForbidden, "engineers cannot manage credentials")
	_, err = cr.List(bob, "acme")
	assert.ErrorIs(t, err, app.ErrForbidden)
	_, err = cr.Set(dave, app.SetCredentialInput{CustomerKey: "globex", Provider: credential.ProviderAnthropic, APIKey: "k"})
	assert.ErrorIs(t, err, app.ErrForbidden, "customer admins only for their customer")

	for name, in := range map[string]app.SetCredentialInput{
		"unknown provider": {CustomerKey: "acme", Provider: "gemini", APIKey: "k"},
		"empty key":        {CustomerKey: "acme", Provider: credential.ProviderAnthropic, APIKey: " "},
		"bad base url":     {CustomerKey: "acme", Provider: credential.ProviderAnthropic, APIKey: "k", BaseURL: "ftp://x"},
	} {
		_, err := cr.Set(dave, in)
		assert.ErrorIs(t, err, app.ErrInvalid, name)
	}
}
