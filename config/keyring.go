// Package config provides configuration management for markedup,
// including secure API key storage via OS-native keychains.
package config

import (
	"crypto/sha256"
	"encoding/hex"
	"log"
	"net/url"
	"strings"

	"github.com/99designs/keyring"
)

const serviceName = "markedup"

// legacyServiceKeyNames lists the historical per-service keyring entries that
// predate endpoint-keyed storage. They are migrated and removed by
// MigrateLegacyKeys on the first launch after upgrade.
var legacyServiceKeyNames = []string{
	"embed-api-key",
	"llm-api-key",
	"rerank-api-key",
	"triplex-api-key",
	"nuextract-api-key",
}

// openRing opens the OS keyring for the markedup service.
func openRing() (keyring.Keyring, error) {
	return keyring.Open(keyring.Config{
		ServiceName: serviceName,
	})
}

// StoreKey saves a named key to the OS keychain under the "markedup" service.
// The name should be produced by KeyNameForEndpoint so multiple services
// sharing one endpoint reuse the same keychain entry.
func StoreKey(name, value string) error {
	ring, err := openRing()
	if err != nil {
		return err
	}
	return ring.Set(keyring.Item{
		Key:  name,
		Data: []byte(value),
	})
}

// GetKey retrieves a named key from the OS keychain.
// Returns ("", nil) if the key is not found.
func GetKey(name string) (string, error) {
	ring, err := openRing()
	if err != nil {
		return "", err
	}
	item, err := ring.Get(name)
	if err != nil {
		if err == keyring.ErrKeyNotFound {
			return "", nil
		}
		return "", err
	}
	return string(item.Data), nil
}

// DeleteKey removes a named key from the OS keychain. Returns nil if the key
// did not exist.
func DeleteKey(name string) error {
	ring, err := openRing()
	if err != nil {
		return err
	}
	if err := ring.Remove(name); err != nil && err != keyring.ErrKeyNotFound {
		return err
	}
	return nil
}

// KeyringAvailable returns true if the OS keyring can be opened successfully.
// Returns false in headless/CI environments where no keychain backend is available.
func KeyringAvailable() bool {
	_, err := openRing()
	return err == nil
}

// NormalizeEndpoint canonicalizes an endpoint URL for keyring keying.
// Lowercases the host, strips default ports (80/443), and removes trailing
// slashes from the path so trivially equivalent URLs hash identically.
// If the input cannot be parsed as a URL, the trimmed lowercase string is
// returned as a stable fallback.
func NormalizeEndpoint(endpoint string) string {
	trimmed := strings.TrimSpace(endpoint)
	if trimmed == "" {
		return ""
	}

	u, err := url.Parse(trimmed)
	if err != nil || u.Host == "" {
		// Fallback: lowercase and strip trailing slashes.
		return strings.TrimRight(strings.ToLower(trimmed), "/")
	}

	host := strings.ToLower(u.Hostname())
	scheme := strings.ToLower(u.Scheme)

	port := u.Port()
	if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
		port = ""
	}

	if port != "" {
		host = host + ":" + port
	}
	u.Host = host
	u.Scheme = scheme
	u.Path = strings.TrimRight(u.Path, "/")
	// Normalize fragment / query off — these should not affect identity.
	u.Fragment = ""
	u.RawQuery = ""

	return u.String()
}

// KeyNameForEndpoint returns the keyring entry name used to store the API key
// for the given endpoint. Format: "apikey-<8 hex chars of sha256(normalized)>".
// Returns "" if the endpoint is empty after normalization.
func KeyNameForEndpoint(endpoint string) string {
	norm := NormalizeEndpoint(endpoint)
	if norm == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(norm))
	return "apikey-" + hex.EncodeToString(sum[:])[:8]
}

// hydrateKeysFromKeyring populates cfg.<Service>.APIKey for any service whose
// APIKey is currently empty by looking up the endpoint-keyed entry. Services
// that share an endpoint share one keyring read (the implementation calls
// GetKey per unique key name). Errors are returned only if every lookup fails;
// individual failures are logged and skipped so a missing/locked keyring
// never blocks config loading.
func hydrateKeysFromKeyring(cfg *Config) {
	if cfg == nil {
		return
	}
	if !KeyringAvailable() {
		return
	}

	// Cache lookups by key name to coalesce duplicate endpoints into one read.
	cache := map[string]string{}
	lookup := func(endpoint string) string {
		name := KeyNameForEndpoint(endpoint)
		if name == "" {
			return ""
		}
		if v, ok := cache[name]; ok {
			return v
		}
		v, err := GetKey(name)
		if err != nil {
			log.Printf("config: keyring lookup failed for %s: %v", name, err)
			cache[name] = ""
			return ""
		}
		cache[name] = v
		return v
	}

	if cfg.Embed.APIKey == "" && cfg.Embed.Endpoint != "" {
		cfg.Embed.APIKey = lookup(cfg.Embed.Endpoint)
	}
	if cfg.LLM.APIKey == "" && cfg.LLM.Endpoint != "" {
		cfg.LLM.APIKey = lookup(cfg.LLM.Endpoint)
	}
	if cfg.Rerank.APIKey == "" && cfg.Rerank.Endpoint != "" {
		cfg.Rerank.APIKey = lookup(cfg.Rerank.Endpoint)
	}
	if cfg.Triplex.APIKey == "" && cfg.Triplex.Endpoint != "" {
		cfg.Triplex.APIKey = lookup(cfg.Triplex.Endpoint)
	}
	if cfg.NuExtract.APIKey == "" && cfg.NuExtract.Endpoint != "" {
		cfg.NuExtract.APIKey = lookup(cfg.NuExtract.Endpoint)
	}
}

// legacyServiceEndpoints maps legacy per-service keyring entry names to the
// endpoint configured for that service in cfg. Returns only services that have
// a non-empty endpoint.
func legacyServiceEndpoints(cfg *Config) map[string]string {
	if cfg == nil {
		return nil
	}
	return map[string]string{
		"embed-api-key":     cfg.Embed.Endpoint,
		"llm-api-key":       cfg.LLM.Endpoint,
		"rerank-api-key":    cfg.Rerank.Endpoint,
		"triplex-api-key":   cfg.Triplex.Endpoint,
		"nuextract-api-key": cfg.NuExtract.Endpoint,
	}
}

// MigrateLegacyKeys consolidates legacy per-service keyring entries
// ("embed-api-key", "llm-api-key", etc.) into the new endpoint-keyed format.
//
// Behavior:
//   - For each legacy entry that exists, look up the configured endpoint for
//     that service. If the endpoint is unknown, leave the legacy entry alone
//     (a later run, with a populated config, can migrate it).
//   - Write the value under KeyNameForEndpoint(endpoint). If the new entry is
//     already populated, do not overwrite (the wizard is the source of truth).
//   - Delete the legacy entry once it has been migrated (or determined to be a
//     duplicate of the already-migrated value).
//
// Idempotent: repeated invocations after migration are no-ops because the
// legacy entries no longer exist.
func MigrateLegacyKeys(cfg *Config) {
	if cfg == nil || !KeyringAvailable() {
		return
	}

	endpoints := legacyServiceEndpoints(cfg)

	for _, legacyName := range legacyServiceKeyNames {
		val, err := GetKey(legacyName)
		if err != nil {
			log.Printf("config: keyring read failed for legacy entry %s: %v", legacyName, err)
			continue
		}
		if val == "" {
			// Legacy entry not present — nothing to migrate.
			continue
		}

		endpoint := endpoints[legacyName]
		if endpoint == "" {
			// We have a legacy key but no endpoint to hash against. Skip and
			// retry on a later launch once the user has configured the service.
			log.Printf("config: keyring migration skipped for %s — no endpoint configured", legacyName)
			continue
		}

		newName := KeyNameForEndpoint(endpoint)
		if newName == "" {
			continue
		}

		existing, err := GetKey(newName)
		if err != nil {
			log.Printf("config: keyring read failed for %s: %v", newName, err)
			continue
		}

		if existing == "" {
			if err := StoreKey(newName, val); err != nil {
				log.Printf("config: keyring migration write failed for %s: %v", newName, err)
				continue
			}
			log.Printf("config: migrated legacy keyring entry %s -> %s", legacyName, newName)
		} else {
			log.Printf("config: legacy keyring entry %s already migrated -> %s", legacyName, newName)
		}

		if err := DeleteKey(legacyName); err != nil {
			log.Printf("config: failed to delete legacy keyring entry %s: %v", legacyName, err)
		}
	}
}

