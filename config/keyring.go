// Package config provides configuration management for markedup,
// including secure API key storage via OS-native keychains.
package config

import (
	"github.com/99designs/keyring"
)

const serviceName = "markedup"

// openRing opens the OS keyring for the markedup service.
func openRing() (keyring.Keyring, error) {
	return keyring.Open(keyring.Config{
		ServiceName: serviceName,
	})
}

// StoreKey saves a named key to the OS keychain under the "markedup" service.
// Key names follow the convention: "embed-api-key", "rerank-api-key",
// "llm-api-key", "triplex-api-key".
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

// DeleteKey removes a named key from the OS keychain.
func DeleteKey(name string) error {
	ring, err := openRing()
	if err != nil {
		return err
	}
	return ring.Remove(name)
}

// KeyringAvailable returns true if the OS keyring can be opened successfully.
// Returns false in headless/CI environments where no keychain backend is available.
func KeyringAvailable() bool {
	_, err := openRing()
	return err == nil
}
