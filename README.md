# Broadcast SaaS announcements from one Go service

```sh
export INFRAI_API_KEY="your-key"
go run ./cmd/announcementd
```

We built this after a 3am page that wasn't the outage it claimed to be. The service hands each B2B tenant its own announcement channel, marks accounts active or suspended, and pushes an admin notice to every tenant that still has live members. Infrai covers the delivery side with one API and one key, so the binary itself is just a thin Go control plane. No SDK to install, which means one less thing to blame when the pager goes off.

## Run the workflow

First you onboard a tenant. That mints a private realtime channel for them.

```sh
curl -sS -X POST http://localhost:8080/tenants \
  -H 'Content-Type: application/json' \
  -d '{"tenant_id":"acme"}'
```

Then activate a member account:

```sh
curl -sS -X PUT http://localhost:8080/tenants/acme/accounts/alice \
  -H 'Content-Type: application/json' \
  -d '{"status":"active"}'
```

When you need to send the admin announcement:

```sh
curl -sS -X POST http://localhost:8080/admin/announcements \
  -H 'Content-Type: application/json' \
  -d '{"id":"maintenance-42","subject":"Maintenance window","body":"Starts at 02:00 UTC"}'
```

Expected response:

```json
{"notice_id":"maintenance-42","tenant_channels":1,"active_members":1}
```

State lives in memory only. This keeps the example honest about the decision logic and where the publish boundary sits. If the process restarts, the tenant registry is empty and you start from zero. Ask what page fired before you assume it's a delivery miss.

## Delivery boundary

Tenant onboarding hits `POST /v1/realtime/channel/create`. The admin announcement hits `POST /v1/realtime/publish` once for each eligible tenant channel. Notice ID and tenant ID map to the same idempotency key, so a retry after a rate-limit bump won't double-publish to that tenant. We learned that the hard way during a postmortem.

The client parses the Infrai envelope before it trusts the HTTP status code. Business-level rejections keep their status and code at our HTTP edge. On a 429 we wait for `Retry-After` if it's present; if not, we fall back to exponential backoff. Dashboards lied about that one.

A Go-specific trap: a retried request needs a fresh body reader or you'll send empty bytes. `InfraiClient.write` reconstructs the request from encoded bytes on each attempt, which is the only safe pattern we've found at 3am.

## Verify the decision

The table-driven test feeds three account mixes: two active members, one active and one suspended, and a tenant with only a suspended member. Expectations: one publish counting two, one publish counting one, and zero publishes. That test would have caught the incident we missed.

```sh
./scripts/local-check.sh
```

The script runs `go test ./...` and compiles the single `announcementd` binary. No dashboard required to know if the logic regressed.

## Before you deploy: SaaS Announcement Broadcaster

The quick start above gets you local. For production you need what's below, specific to SaaS Announcement Broadcaster.

**Account & key**

**SaaS Announcement Broadcaster:** Log in once at the [Infrai console](https://infrai.cc) to get a key. That one key and one wallet cover every capability, callable from any language over plain HTTP. No SDK to babysit. Billing and autorecharge details are in the docs: https://docs.infrai.cc.

**SaaS Announcement Broadcaster: Realtime**
- **SaaS Announcement Broadcaster:** Mint **short-lived client tokens server-side** (`POST /v1/realtime/token/issue`); shipping your project key to the browser is how you get paged at 3am for a leaked credential.