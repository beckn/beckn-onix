# Security Policy

Beckn-ONIX is a plugin-based middleware adapter for the Beckn Protocol. If you discover a security vulnerability in this repository, please report it responsibly. Do not open a public GitHub issue that includes vulnerability details, proof-of-concept code, or exploit instructions.

## Supported Versions

This repository does not currently document a supported-version matrix. When reporting an issue, please include the affected commit hash, release tag, or version whenever possible so maintainers can identify the impacted code.

## Reporting a Vulnerability

This repository does not currently provide a dedicated security email address or a private vulnerability reporting mechanism.

Please do not disclose security vulnerabilities publicly until maintainers have had a reasonable opportunity to investigate.

When reporting a vulnerability, include as much of the following as you can:

- Affected version, release tag, or commit hash
- Repository path (file, package, or plugin)
- Root cause
- Impact
- Reproduction steps
- Proof of concept (if available)
- Suggested remediation

Do not include exploit details, sensitive data, or full proof-of-concept code in public GitHub issues.

Until a dedicated reporting channel is available, open a minimal public issue asking maintainers to provide a confidential communication channel. Do not include vulnerability details in that issue.

## Coordinated Disclosure

Beckn-ONIX maintainers intend to work collaboratively with security researchers on valid reports. After a fix is available, maintainers may acknowledge reports publicly (for example, in release notes or a security advisory), with credit to the reporter when appropriate and agreed upon.

No specific acknowledgment or publication timeline is guaranteed.

## Scope

This policy applies only to the [beckn-onix](https://github.com/beckn/beckn-onix) repository and its source code, build tooling, and documentation maintained in this project.

It does not cover:

- The Beckn Protocol specification or other Beckn Foundation projects
- ONDC or other network deployments that use Beckn-ONIX
- Downstream forks, integrations, or vendor distributions
- Third-party infrastructure, hosted services, or production deployments operated by others

Reports about deployments you do not own or operate should be directed to the relevant operator.

## Good Faith Research

Responsible security research against the source code in this repository is welcome. This includes static analysis, local testing, and review of plugins and configuration examples shipped in the repository.

Do not test against production systems, live networks, or third-party infrastructure without explicit authorization from the system owner.

## Future Improvements

Maintainers are encouraged to strengthen this policy over time by adding:

- A dedicated security contact email
- [GitHub Private Vulnerability Reporting](https://docs.github.com/en/code-security/security-advisories/working-with-repository-security-advisories/configuring-private-vulnerability-reporting-for-a-repository)
- A documented supported-version policy
- A coordinated disclosure timeline
- A documented security advisory workflow
