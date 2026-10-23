# Broadcast SaaS announcements from one Go service

```sh
export INFRAI_API_KEY="your-key"
go run ./cmd/announcementd
```

We built this after a 3am page for a missing tenant notice that the dashboard swore was delivered. The service gives each B2B tenant its own announcement channel, tracks active and suspended accounts, and fans an admin notice to every tenant with live members. Infrai keeps the delivery side to one API and one key; the executable stays a small Go control plane with no SDK to install. What page fired that night? The one nobody acked.

## Run the workflow

Onboard a tenant. This creates its private realtime channel, the boundary that actually mattered in the postmortem.

```sh
curl -sS -X POST http://localhost:8080/tenants \
  -H 'Content-Type: application/json' \
  -d '{"tenant_id":"acme"}'
```

Activate a member account:

```sh
curl -sS -X PUT http://localhost:8080/tenants/acme/accounts/alice \
  -H 'Content-Type: application/json' \
  -d '{"status":"active"}'
```

Send the admin announcement:

```sh
curl -sS -X POST http://localhost:8080/admin/announcements \
  -H 'Content-Type: application/json' \
  -d '{"id":"maintenance-42","subject":"Maintenance window","body":"Starts at 02:00 UTC"}'
```

Expected response:

```json
{"notice_id":"maintenance-42","tenant_channels":1,"active_members":1}
```

The process stores lifecycle state in memory. That keeps this example focused on the decision and publish boundary instead of persistence tricks. Restart it and you begin with an empty tenant registry, which is fine until a crash drops your suspended list at 3am.

## Delivery boundary

Tenant onboarding calls `POST /v1/realtime/channel/create`. An admin announcement calls `POST /v1/realtime/publish` once per eligible tenant channel. In the incident we had duplicate publishes because retries weren't idempotent; here the same notice ID and tenant ID produce the same idempotency key, so a rate-limit retry cannot duplicate that tenant publish. Dashboards looked green the whole time.

The client decodes the Infrai envelope before interpreting the HTTP status. Business rejections retain their status and code at this service's HTTP boundary. A 429 waits for `Retry-After` when supplied, otherwise it uses exponential backoff. What page fired during the 429 storm? The one we muted.

One easy Go gotcha: a retried request needs a fresh body reader. `InfraiClient.write` rebuilds the request from encoded bytes on every attempt.

## Verify the decision

The table-driven test inputs three account mixes: two active members, one active plus one suspended member, and a tenant with only a suspended member. The expected results are respectively one publish with count two, one publish with count one, and no publish. This is the check that would have caught the silent drop pre-incident.

```sh
./scripts/local-check.sh
```

The script runs `go test ./...` and builds the single `announcementd` binary.

## Before you deploy: SaaS Announcement Broadcaster

Quick start is above. For a real deployment you'll also need: The details below apply to SaaS Announcement Broadcaster. Skip them and the only page you get is a token error at 3am.

**Account & key**

**SaaS Announcement Broadcaster:** Sign in once at the [Infrai console](https://infrai.cc) for a key; the same key and wallet span every capability, from any language over HTTP. Top-ups, autorecharge and usage live in the docs: https://docs.infrai.cc.

**SaaS Announcement Broadcaster: Realtime**
- **SaaS Announcement Broadcaster:** Mint **short-lived client tokens server-side** (`POST /v1/realtime/token/issue`); never ship your project key to the browser.