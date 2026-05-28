# Security Policy

## Reporting a Vulnerability

If you discover a security vulnerability in Gen Code, please report it
privately via GitHub's built-in reporting system:

1. Go to the [Security Advisories][advisories] tab
2. Click **Report a vulnerability**
3. Fill in the form with a clear description of the issue and steps to reproduce

We make a best effort to acknowledge reports within a few business days and to
follow up with an initial assessment shortly after. Response times depend on
maintainer availability.

### What to include

- A detailed description of the vulnerability
- Steps to reproduce (ideally a minimal proof of concept)
- Affected versions (or commit range)
- Any known mitigations

### Disclosure

We follow a coordinated disclosure process:

- A GitHub Security Advisory (GHSA) will be created to track the issue
- Patches are developed in private, temporary forks
- A CVE will be requested if warranted
- Credits are given to the reporter (unless anonymity is requested)

## Supported Versions

Only the latest release receives security patches. We recommend always using
the most recent version.

## Security Model

Gen Code is a CLI tool that:

- Reads and writes files to your local filesystem
- Sends prompts and code context to LLM providers (Anthropic, OpenAI, Google)
- Stores session transcripts locally in `~/.gen/`

**Do not** use Gen Code with untrusted extensions, MCP servers, or hooks
without auditing them first.

[advisories]: https://github.com/genai-io/gen-code/security/advisories
