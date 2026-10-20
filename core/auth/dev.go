//go:build dev

package auth

// Dev builds (go build -tags dev) let DDASH_DEV_AUTH=1 sign every request in
// as operator:dev, so make dev runs without Keycloak. Release builds don't
// contain this file.
func init() { devBypass = As("operator:dev") }
