// Package anthropic reads keys and spend from the Anthropic Admin API
// (api.anthropic.com/v1/organizations/...) using an Admin key.
//
// Shapes follow platform.claude.com/docs (API reference), checked 2026-10-21:
//   - GET  /organizations/me                       cheap validation call
//   - GET  /organizations/api_keys                 partial_key_hint, status, scope.workspace_id (after_id/has_more/last_id)
//   - POST /organizations/api_keys/{id}            {"status": "inactive"|"active"}, reversible
//   - GET  /organizations/usage_report/messages    tokens per key/model; 1d (max 31/page), 1h (max 168/page); has_more/next_page
//
// The cost report is daily and grouped by workspace, not key, so cost per key
// is tokens × the bundled price table and always Approx.
//
// Spend limits: Console workspace spend limits are set in the Console UI and
// are not exposed by the Admin API (the Workspace object has no limit field).
// The Spend Limits API is Claude Enterprise only and per member, not per
// workspace. So Uncapped returns nil, nil.
//
// The Admin API needs an organization; individual accounts can't create
// Admin keys.
package anthropic
