# DNS Verification

## When to Run

For every service that has a DNS hostname configured (e.g., `<subdomain>.<domain>`).

## How to Check

```bash
# Resolve the hostname
dig +short <hostname>

# Check CNAME records
dig +short CNAME <hostname>

# Verify against expected IP
dig +short A <hostname>
```

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
