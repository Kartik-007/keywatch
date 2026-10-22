// Package railway reads variables from every Railway project, environment and
// service the logged-in Railway CLI user can see, through Railway's public
// GraphQL API. Sealed variables can't be read back, so AI-looking ones are
// reported by name only.
package railway
