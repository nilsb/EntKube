# EntKube

A multi-tenant platform for managing shared Kubernetes applications and infrastructure services.

[![License: AGPL v3](https://img.shields.io/badge/License-AGPL%20v3-blue.svg)](LICENSE)

## Vision

EntKube provides a unified developer portal for provisioning, configuring, and monitoring shared services running on Kubernetes — such as MinIO, CloudNativePG, Keycloak, and more. Teams get self-service access to the infrastructure they need without managing the underlying clusters directly.

## Key Features

- **Shared Application Management** — Deploy, configure, and lifecycle-manage shared services (MinIO, CNPG, Keycloak, etc.) across clusters.
- **Multi-Tenant SaaS** — Isolated tenants with role-based access, resource quotas, and per-team service instances.
- **Kubernetes Cluster Management** — Register, monitor, and operate multiple clusters from a single control plane.
- **Observability** — Health dashboards, Prometheus metrics, Loki log browsing, alerts, and escalation routing.
- **Identity & Secrets** — Keycloak integration for SSO, Vault-backed secret encryption.
- **Storage & Databases** — Self-service PostgreSQL (CNPG), MongoDB, Redis, RabbitMQ, and S3-compatible object storage.
- **Git Sync** — GitOps-style deployment sync, webhook support, and App-of-Apps pattern.
- **Developer Portal** — Self-service UI for teams to request and manage service instances, view SLA reports, and track incidents.
- **CI/CD Integration** — Automated build and deploy via GitHub Actions on every push to `main`.

## Tech Stack

| Layer | Technology |
|---|---|
| Frontend | Blazor (.NET 10) — Server + WebAssembly |
| Backend | ASP.NET Core 10 |
| Auth | ASP.NET Core Identity + Keycloak (SSO) + passkey support |
| Database | SQLite (dev) / PostgreSQL (prod) via Entity Framework Core 10 |
| Secret encryption | HashiCorp Vault |
| Kubernetes | Official .NET K8s client, kubectl, Helm |
| Observability | Prometheus, Loki, OnCall |
| Messaging | RabbitMQ |
| Caching | Redis |
| Object storage | MinIO / OpenStack S3 |
| Container registry | Harbor / Azure Container Registry |
| Reverse proxy | Caddy (automatic TLS via Let's Encrypt) |

## Prerequisites

- [.NET 10 SDK](https://dotnet.microsoft.com/download) with the `wasm-tools` workload
- [Docker](https://docs.docker.com/get-docker/) (for the full stack via Compose)
- A running PostgreSQL instance (or use SQLite for local development)

```bash
dotnet workload install wasm-tools
```

## Getting Started

```bash
git clone https://github.com/nilsb/EntKube.git
cd EntKube
dotnet run --project src/EntKube.Web
```

The app launches at `https://localhost:7001` (see `src/EntKube.Web/Properties/launchSettings.json`).

## Project Structure

```
src/
├── EntKube.Web/           # ASP.NET Core host — Blazor Server pages, services, EF Core, Identity
│   ├── Authorization/     # Custom policy requirements
│   ├── Components/        # Razor components and pages
│   │   └── Pages/
│   │       ├── Admin/     # User, role, notification, and backup management
│   │       ├── Portal/    # Customer-facing portal (status, incidents, deployments)
│   │       └── Tenants/   # Full tenant management UI (clusters, apps, databases, storage…)
│   ├── Data/              # EF Core DbContext and migrations
│   └── Services/          # ~57 domain services (Kubernetes, Git, Vault, monitoring, etc.)
└── EntKube.Web.Client/    # Blazor WebAssembly client project

tests/
└── EntKube.Web.Tests/     # xUnit tests
```

## Running Tests

```bash
dotnet test tests/EntKube.Web.Tests
```

## Docker

### Build and push

> **Apple Silicon (M-series) Mac:** servers are typically `linux/amd64`. Use `docker buildx` to cross-compile and push in one step — `--push` is required because multi-platform images cannot be loaded into the local Docker daemon.

```bash
# One-time setup
docker buildx create --use --name multiarch

# Build for amd64 and push
docker buildx build --platform linux/amd64 \
  -t <your-registry>/entkube:latest \
  --push .

# Tag a versioned release
docker buildx build --platform linux/amd64 \
  -t <your-registry>/entkube:latest \
  -t <your-registry>/entkube:1.0.0 \
  --push .
```

### Run with Docker Compose

Copy `.env.example` to `.env`, fill in the required values, then start the stack:

```bash
cp .env.example .env
# edit .env

docker compose up -d
```

The app will be available at `http://localhost:8080` (Caddy terminates TLS and proxies to the app).

> **Data safety** — `docker compose down` does **not** delete the database volume.
> Only `docker compose down -v` removes volumes.

### Required environment variables

| Variable | Description |
|---|---|
| `DOMAIN` | Public domain pointing at the server (e.g. `entkube.example.com`). |
| `ACME_EMAIL` | Email for Let's Encrypt account and expiry notices. |
| `REGISTRY` | Container registry hostname (e.g. `entit.azurecr.io`). |
| `IMAGE_TAG` | Image tag to deploy (e.g. `latest`). |
| `REGISTRY_USERNAME` | Registry username or service principal ID. |
| `REGISTRY_PASSWORD` | Registry password or service principal secret. |
| `POSTGRES_PASSWORD` | Password for the PostgreSQL database. |
| `VAULT__ROOTKEY` | 32-byte base64 key for secret encryption. Generate with `openssl rand -base64 32`. |

### TLS / HTTPS

Caddy handles TLS automatically. On first start it obtains a Let's Encrypt certificate for `DOMAIN` and renews it automatically. Ports **80** and **443** must be reachable from the internet and your DNS must point at the server before starting the stack.

## CI/CD

The `.github/workflows/deploy.yml` workflow runs on every push to `main`:

1. Builds and pushes a `linux/amd64` Docker image to the configured container registry (tagged with the short commit SHA and `latest`).
2. SSHs into the production server, pulls the new image, and runs `docker compose up -d --remove-orphans`.

## License

[GNU Affero General Public License v3.0 or later](LICENSE) — see the license file for details.

Copyleft applies: if you run a modified version of EntKube as a network service, you must make the modified source code available to users of that service.
