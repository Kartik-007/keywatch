// Package openai reads keys and spend from the OpenAI Admin API
// (api.openai.com/v1/organization/...) using an organization admin key.
//
// Shapes follow github.com/openai/openai-openapi (openapi.yaml), checked
// 2026-10-21:
//   - GET  /organization/projects                         list projects (after/has_more/last_id)
//   - GET  /organization/projects/{id}/api_keys           redacted_value, created_at, last_used_at, expires_at
//   - DELETE /organization/projects/{id}/api_keys/{key}   irreversible
//   - GET  /organization/costs?group_by[]=api_key_id      exact USD per key, 1d buckets only
//   - GET  /organization/usage/completions                tokens per key/model, 1m/1h/1d buckets
//   - GET  /organization/spend_limit, /organization/projects/{id}/spend_limit
//     hard spend limits; 404 means none is set
//
// List query params use bracket notation (group_by[]=a&group_by[]=b), the
// same encoding the official SDKs send.
package openai
