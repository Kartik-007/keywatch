# Security

keywatch reads your AI API keys, so we take security reports seriously.

## How to report

Use GitHub's private vulnerability reporting: go to the [Security tab](https://github.com/Kartik-007/keywatch/security) and click **Report a vulnerability**, or open [a new advisory](https://github.com/Kartik-007/keywatch/security/advisories/new) directly.

**Never open a public issue for a security problem.** And never paste a real key anywhere, including in a private report. The prefix and last 4 characters (like `sk-proj-…a91f`) are enough.

## What's in scope

- Anything that could print, log, store or transmit a key value, in full or in part beyond the redacted form keywatch shows.
- Network calls to hosts other than the provider and platform APIs listed in the README.
- The GitHub workflow that `keywatch watch` generates, including how it handles secrets.
- The release and install path: GitHub Releases, the goreleaser config, and the Homebrew tap.

## What's not in scope

- Bugs in OpenAI, Anthropic, Railway, Vercel or GitHub themselves. Report those to the vendor.
- Keys that keywatch correctly finds in your own files or platforms. That's the tool working.
- Attacks that need someone who already controls your machine or your logged-in CLI sessions.
- Missed detections or false positives with no security impact. Use the [detection issue form](https://github.com/Kartik-007/keywatch/issues/new?template=detection.yml) for those.

## What to expect

- An acknowledgement within 3 days.
- A fix, or a plan with a timeline, within 14 days.
- Credit in the release notes if you want it.

## Supported versions

Only the latest release gets security fixes. Please check you're on it before reporting.
