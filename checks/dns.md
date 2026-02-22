# DNS Verification

## When to Run

For every service that has a DNS hostname configured (e.g., `<subdomain>.<domain>`).

## How to Check

<!-- Governing: SPEC-0002 REQ-5 — Embedded Command Examples -->

```bash
# Resolve the hostname
dig +short <hostname>

# Check CNAME records
dig +short CNAME <hostname>

# Verify against expected IP
dig +short A <hostname>
```

Replace `<hostname>` with the service's actual DNS name from the inventory (e.g., `jellyfin.example.com`). If the inventory provides an expected IP, compare the resolved value against it.

## What's Healthy

- Hostname resolves to the expected IP address or CNAME
- Resolution completes within a reasonable time

## What's Unhealthy

- NXDOMAIN (hostname doesn't exist)
- Resolves to wrong IP/CNAME
- Resolution timeout
- SERVFAIL (DNS server error)

## What to Record

- Hostname queried
- Resolved value (IP or CNAME)
- Whether it matches the expected value
- Resolution time
