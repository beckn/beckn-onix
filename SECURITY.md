# Security Policy

Beckn-ONIX is a plugin-based middleware adapter for the Beckn Protocol. This document describes how to report security vulnerabilities found in this repository.

## Supported Versions

Only the latest published release is supported for security fixes. We ship frequently (typically every few weeks), so fixes go into the next release rather than being backported to older release lines.

If you're not on the latest tag, please upgrade before or shortly after filing a report so the fix reaches you.

## Reporting a Vulnerability

Report vulnerabilities privately using [GitHub Private Vulnerability Reporting](https://github.com/beckn/beckn-onix/security/advisories/new):

1. Go to the repository's **Security** tab.
2. Click **Report a vulnerability**.
3. Fill in the advisory form with as much detail as possible.

Do not open a public GitHub issue for security vulnerabilities.

Include, where possible:

- Affected version, release tag, or commit hash
- Affected file, package, or plugin
- Root cause and impact
- Reproduction steps and, if available, proof-of-concept code
- Suggested remediation

### Response Targets

- **Acknowledgement:** within 3 business days of submission.
- **Initial triage/assessment:** within 7 business days.
- **Fix timeline:** communicated to the reporter after triage, based on severity.

These are targets, not contractual SLAs.

## Coordinated Disclosure

We follow coordinated disclosure. Once a fix is available, we will:

- Publish a GitHub Security Advisory describing the issue and affected versions.
- Credit the reporter, if they wish to be credited.
- Default to a 90-day disclosure window from initial report, adjustable by mutual agreement between maintainers and the reporter if a complex fix needs more time.

Please do not publicly disclose a vulnerability before a fix is released or the agreed disclosure window elapses.

## Scope

This policy covers the source code, build tooling, and documentation in the [beckn-onix](https://github.com/beckn/beckn-onix) repository.

Out of scope:

- The Beckn Protocol specification and other Beckn Foundation repositories
- Any networks and their participants running Beckn-ONIX in production
- Downstream forks, integrations, or vendor distributions
- Third-party infrastructure not operated by this project

Reports about a live deployment you don't operate should go to that deployment's operator, not this repository.

## Good Faith Research

We support good-faith security research against this repository's source code — static analysis, local testing, and review of the plugins and example configs shipped here. We will not pursue legal action against researchers who:

- Make a good-faith effort to avoid privacy violations, data destruction, and service disruption.
- Report findings through the private channel above rather than publicly.
- Do not test against production systems, live Beckn networks, or infrastructure they don't own or have explicit authorization to test.
