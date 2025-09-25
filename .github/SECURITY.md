# Security Policy

## Supported Versions

We release patches for security vulnerabilities in the following versions:

| Version | Supported          |
| ------- | ------------------ |
| 0.1.x   | :white_check_mark: |
| 0.0.x   | :white_check_mark: |

## Reporting a Vulnerability

If you discover a security vulnerability in vb-cli, please report it responsibly:

1. **Do not** open a public issue
2. Email security details to: security@virtualboard.dev
3. Include the following information:
   - Description of the vulnerability
   - Steps to reproduce
   - Potential impact
   - Suggested fix (if any)

## Response Timeline

- We will acknowledge receipt within 48 hours
- We will provide a detailed response within 7 days
- We will keep you informed of our progress
- We will coordinate public disclosure if needed

## Security Measures

This project implements several security measures:

- **Dependency Scanning**: Automated scanning with gosec
- **Code Coverage**: 100% test coverage requirement
- **Secure File Permissions**: Proper file permission handling
- **Input Validation**: JSON schema validation for all inputs
- **Atomic Operations**: Safe file operations to prevent corruption

## Security Updates

Security updates are released as patch versions following semantic versioning. Critical security fixes may be backported to previous major versions.

## Responsible Disclosure

We follow responsible disclosure practices and will:
- Work with you to understand and resolve the issue
- Give credit for responsible disclosure (unless you prefer to remain anonymous)
- Coordinate public disclosure timing
- Release fixes as quickly as possible
