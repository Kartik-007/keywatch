// Package vercel reads environment variables from every Vercel project the
// logged-in Vercel CLI user can see, across their personal scope and teams.
// Sensitive variables can't be decrypted, so AI-looking ones are reported by
// name only.
package vercel
