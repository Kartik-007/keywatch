// Package creds stores provider admin keys in the OS keychain. Environment
// variables win, so the GitHub Action can pass keys as encrypted secrets.
package creds

import (
	"os"
	"strings"

	"github.com/zalando/go-keyring"
)

const service = "keywatch"

func envVar(provider string) string {
	return "KEYWATCH_" + strings.ToUpper(provider) + "_ADMIN_KEY"
}

// Get returns the admin key for a provider, or "" if not connected.
func Get(provider string) string {
	if v := os.Getenv(envVar(provider)); v != "" {
		return v
	}
	v, err := keyring.Get(service, provider)
	if err != nil {
		return ""
	}
	return v
}

func Set(provider, key string) error { return keyring.Set(service, provider, key) }

// Forget removes a stored key. Missing keys are not an error.
func Forget(provider string) error {
	if err := keyring.Delete(service, provider); err != nil && err != keyring.ErrNotFound {
		return err
	}
	return nil
}

// EnvVar is exported so the watch workflow can name the secret it sets.
func EnvVar(provider string) string { return envVar(provider) }
