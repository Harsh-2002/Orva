package llm

import (
	"context"
	"fmt"
	"strings"

	"github.com/maximhq/bifrost/core/schemas"
)

// KeyResolver supplies provider credentials to the gateway on demand. It is
// implemented over Orva's ai_provider_configs table (keys decrypted via the
// secrets cipher). Lookups are live, so a freshly-added key is picked up the
// next time the gateway resolves it — no restart.
type KeyResolver interface {
	// Providers returns the providers that currently have an enabled key.
	Providers() []string
	// Resolve returns the decrypted API key and optional base-URL override for
	// a provider, or an error if none is configured.
	Resolve(provider string) (apiKey, baseURL string, err error)
}

// account adapts a KeyResolver to Bifrost's schemas.Account interface.
type account struct{ resolver KeyResolver }

func (a *account) GetConfiguredProviders() ([]schemas.ModelProvider, error) {
	names := a.resolver.Providers()
	out := make([]schemas.ModelProvider, 0, len(names))
	for _, n := range names {
		out = append(out, schemas.ModelProvider(strings.ToLower(n)))
	}
	return out, nil
}

func (a *account) GetKeysForProvider(_ context.Context, providerKey schemas.ModelProvider) ([]schemas.Key, error) {
	apiKey, _, err := a.resolver.Resolve(string(providerKey))
	if err != nil {
		return nil, fmt.Errorf("no key configured for provider %q: %w", providerKey, err)
	}
	return []schemas.Key{{
		ID:     string(providerKey),
		Value:  schemas.EnvVar{Val: apiKey},
		Models: schemas.WhiteList{"*"}, // this key may serve any model (empty list = none allowed)
		Weight: 1.0,
	}}, nil
}

func (a *account) GetConfigForProvider(providerKey schemas.ModelProvider) (*schemas.ProviderConfig, error) {
	cfg := &schemas.ProviderConfig{
		ConcurrencyAndBufferSize: schemas.ConcurrencyAndBufferSize{Concurrency: 3, BufferSize: 10},
	}
	if _, baseURL, err := a.resolver.Resolve(string(providerKey)); err == nil && baseURL != "" {
		cfg.NetworkConfig.BaseURL = baseURL
	}
	return cfg, nil
}
