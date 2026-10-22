// Package watch is the always-on part of keywatch: the GitHub Actions
// workflow that runs every hour, and the stateless check it runs.
package watch

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/Kartik-007/keywatch/internal/core"
	"github.com/Kartik-007/keywatch/internal/creds"
)

// File is where the workflow lives in the user's repo.
const File = ".github/workflows/keywatch.yml"

// WebhookEnv is the repo secret (and env var in the Action) holding an
// optional Slack or Discord incoming webhook URL.
const WebhookEnv = "KEYWATCH_WEBHOOK"

// VarsEnv carries the repo's Actions variables, as JSON, into the job. Acks
// live there as KEYWATCH_ACK_<ID>.
const VarsEnv = "KEYWATCH_VARS"

// ReleaseVersion is the keywatch release the workflow installs, without the
// leading "v". The CLI sets it to its own version when that is a release.
var ReleaseVersion = "0.1.0"

// Repo is where releases are downloaded from.
const Repo = "Kartik-007/keywatch"

var notVarChar = regexp.MustCompile(`[^A-Z0-9_]`)

// AckVar is the repo variable that silences alerts for a key id (or
// anomaly.Org). GitHub variable names allow only A-Z, 0-9 and _, and are
// stored upper case, so other characters become _.
func AckVar(id string) string {
	return "KEYWATCH_ACK_" + notVarChar.ReplaceAllString(strings.ToUpper(id), "_")
}

// Workflow returns the contents of .github/workflows/keywatch.yml. It runs
// hourly (and on demand), installs a pinned, checksum-verified keywatch
// release, and runs `keywatch watch run`. Admin keys come from encrypted repo
// secrets; ack state comes from repo variables.
func Workflow(scaryDay float64, providers []string, webhookSecret bool) string {
	var env []string
	for _, p := range []string{core.OpenAI, core.Anthropic} {
		for _, q := range providers {
			if q == p {
				name := creds.EnvVar(p)
				env = append(env, fmt.Sprintf("%s: ${{ secrets.%s }}", name, name))
			}
		}
	}
	if webhookSecret {
		env = append(env, fmt.Sprintf("%s: ${{ secrets.%s }}", WebhookEnv, WebhookEnv))
	}
	env = append(env, VarsEnv+": ${{ toJSON(vars) }}")

	var b strings.Builder
	b.WriteString(`# Written by ` + "`keywatch watch`" + `. Every hour this checks what each AI key spent.
# When spend looks wrong the job fails, so GitHub emails you within the hour.
# Re-run ` + "`keywatch watch`" + ` to change it. Source: https://github.com/` + Repo + `
name: keywatch
on:
  schedule:
    # A few minutes past the hour, so providers have reported the last hour.
    - cron: "17 * * * *"
  workflow_dispatch:
permissions:
  contents: read
  actions: read
concurrency:
  group: keywatch
jobs:
  watch:
    runs-on: ubuntu-latest
    timeout-minutes: 10
    env:
      KEYWATCH_VERSION: "` + ReleaseVersion + `"
    steps:
      - uses: actions/checkout@v4
        with:
          persist-credentials: false
      - name: Install keywatch
        run: |
          set -euo pipefail
          cd "$RUNNER_TEMP"
          base="https://github.com/` + Repo + `/releases/download/v${KEYWATCH_VERSION}"
          tarball="keywatch_${KEYWATCH_VERSION}_linux_amd64.tar.gz"
          sums="keywatch_${KEYWATCH_VERSION}_checksums.txt"
          curl -fsSLO "$base/$tarball"
          curl -fsSLO "$base/$sums"
          grep " ${tarball}\$" "$sums" | sha256sum --check --strict
          mkdir -p bin
          tar -xzf "$tarball" -C bin keywatch
          echo "$RUNNER_TEMP/bin" >> "$GITHUB_PATH"
      - name: Check spend
        run: keywatch watch run --scary-day ` + strconv.FormatFloat(scaryDay, 'f', -1, 64) + `
        env:
`)
	for _, e := range env {
		b.WriteString("          " + e + "\n")
	}
	return b.String()
}
