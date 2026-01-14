# Security Policy

## Reporting a Vulnerability

The Formbricks Hub team takes security vulnerabilities seriously. We appreciate your efforts to responsibly disclose your findings.

### How to Report

**Please DO NOT report security vulnerabilities through public GitHub issues.**

Instead, please report security vulnerabilities by email to:

**security@formbricks.com**

### What to Include

To help us better understand and resolve the issue, please include as much of the following information as possible:

- **Type of vulnerability** (e.g., SQL injection, authentication bypass, etc.)
- **Full paths of source file(s)** related to the vulnerability
- **Location of the affected source code** (tag/branch/commit or direct URL)
- **Step-by-step instructions** to reproduce the issue
- **Proof-of-concept or exploit code** (if possible)
- **Impact of the vulnerability** (what an attacker could achieve)
- **Any special configuration** required to reproduce the issue

### Response Timeline

- **Acknowledgment**: We will acknowledge receipt of your vulnerability report within **48 hours**
- **Updates**: We will provide regular updates about our progress at least every **7 days**
- **Resolution**: We aim to resolve critical vulnerabilities within 30 days
- **Disclosure**: We will coordinate with you on the disclosure timeline after a fix is available

### What to Expect

1. We will confirm the vulnerability and determine its impact
2. We will develop and test a fix
3. We will release a security update
4. We will publicly disclose the vulnerability after users have had time to update
5. We will credit you for the discovery (unless you prefer to remain anonymous)

## Supported Versions

Formbricks Hub is currently in **beta** (pre-1.0 release). We provide security updates for:

| Version | Supported          |
| ------- | ------------------ |
| Latest  | :white_check_mark: |
| < Latest| :x:                |

We recommend always using the latest version of Formbricks Hub.

## Security Best Practices

### For Users

When deploying Formbricks Hub, please follow these security best practices:

#### API Keys

- **Generate strong API keys**: Use cryptographically secure random strings (at least 32 characters)
- **Store securely**: Never commit API keys to version control
- **Use environment variables**: Store keys in `.env` files or secret management systems
- **Rotate regularly**: Change API keys periodically, especially if compromised
- **Limit scope**: Use different API keys for different environments (dev, staging, prod)

#### OpenAI Keys

- **Protect your OpenAI key**: It's used for AI enrichment and has associated costs
- **Monitor usage**: Check OpenAI dashboard for unexpected usage patterns
- **Set billing limits**: Configure spending limits in OpenAI dashboard
- **Revoke if leaked**: Immediately revoke and regenerate if exposed

#### Database Security

- **Strong passwords**: Use complex passwords for database access
- **Network isolation**: Limit database access to necessary services only
- **Encrypted connections**: Use SSL/TLS for database connections in production
- **Regular backups**: Implement automated backup strategy
- **Update regularly**: Keep PostgreSQL updated to the latest patch version

#### Docker/Container Security

- **Don't run as root**: Use non-root users in containers when possible
- **Keep images updated**: Regularly update base images and dependencies
- **Scan for vulnerabilities**: Use tools like Trivy or Snyk to scan images
- **Limit network exposure**: Only expose necessary ports
- **Use secrets management**: Don't embed secrets in images

#### Webhook Security

- **Verify signatures**: Enable webhook signature verification when available (coming soon)
- **Use HTTPS**: Only send webhooks to HTTPS endpoints
- **Implement retries carefully**: Be aware of replay attack vectors
- **Validate payloads**: Always validate incoming webhook data

### For Contributors

When contributing code to Formbricks Hub:

- **No hardcoded secrets**: Never commit API keys, passwords, or sensitive data
- **Validate inputs**: Always validate and sanitize user inputs
- **Use parameterized queries**: Prevent SQL injection (Ent ORM handles this)
- **Handle errors safely**: Don't expose sensitive information in error messages
- **Review dependencies**: Be cautious when adding new dependencies
- **Follow secure coding practices**: Use static analysis tools (golangci-lint)

## Known Security Considerations

### Current Limitations

1. **API Key Authentication**: Currently uses a single shared API key
   - **Mitigation**: Use different keys per environment
   - **Future**: Multi-key support with granular permissions planned

2. **Webhook Signature Verification**: Not yet implemented
   - **Mitigation**: Use internal network or VPN for webhook endpoints
   - **Future**: HMAC signature verification coming in v0.2.0

3. **Rate Limiting**: No built-in rate limiting on API endpoints
   - **Mitigation**: Use reverse proxy (Nginx, Caddy) with rate limiting
   - **Future**: Native rate limiting planned

### No PII Storage by Default

Formbricks Hub is designed to avoid storing Personally Identifiable Information (PII):

- Use hashed `user_identifier` instead of email addresses or names
- Store only necessary feedback data
- Implement data retention policies appropriate for your use case
- Consider GDPR/CCPA requirements for your implementation

## Security Updates

Security updates will be released as:

1. **GitHub Security Advisories**: Critical vulnerabilities
2. **GitHub Releases**: All security patches with detailed changelogs
3. **Docker Images**: Updated images published to `ghcr.io`

Subscribe to our GitHub repository to receive notifications about security updates.

## Scope

This security policy applies to:

- Formbricks Hub API
- Data import scripts (`scripts/data-imports/*`)
- Documentation site (`docs/`)
- Docker configurations
- Dependencies and third-party libraries

Out of scope:

- Third-party services (OpenAI, G2, etc.)
- User-modified configurations
- Issues in forked repositories
- The `.github/` folder itself (excluding policies)

## Bug Bounty Program

We currently do **not** have a bug bounty program. However, we deeply appreciate security research and will publicly credit researchers who responsibly disclose vulnerabilities (with their permission).

## Contact

For security concerns, contact: **security@formbricks.com**

For general questions, use GitHub Discussions or Issues.

---

Thank you for helping keep Formbricks Hub and our users safe!

