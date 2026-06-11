# Contributing to EntKube

Thank you for your interest in contributing! This document covers how to set up your development environment, the workflow for submitting changes, and the conventions we follow.

## Table of Contents

- [Prerequisites](#prerequisites)
- [Development Setup](#development-setup)
- [Project Structure](#project-structure)
- [Running the App](#running-the-app)
- [Running Tests](#running-tests)
- [Making Changes](#making-changes)
- [Submitting a Pull Request](#submitting-a-pull-request)
- [License](#license)

## Prerequisites

- [.NET 10 SDK](https://dotnet.microsoft.com/download) with the `wasm-tools` workload
- [Docker](https://docs.docker.com/get-docker/) and Docker Compose
- [Git](https://git-scm.com/)
- A PostgreSQL instance for integration work (or SQLite works out of the box for local dev)

```bash
dotnet workload install wasm-tools
```

## Development Setup

```bash
git clone https://github.com/nilsb/EntKube.git
cd EntKube
cp .env.example .env   # fill in values if you need the full stack
dotnet restore
```

For local development the app defaults to SQLite, so no database setup is required to get started.

## Project Structure

```
src/
├── EntKube.Web/           # Server-side host — all services, EF Core, Identity, Blazor pages
│   ├── Authorization/     # Custom ASP.NET Core authorization requirements
│   ├── Components/Pages/  # Razor pages, organized by area (Admin, Portal, Tenants)
│   ├── Data/              # DbContext, EF Core migrations
│   └── Services/          # Domain services (Kubernetes, Git, Vault, monitoring, etc.)
└── EntKube.Web.Client/    # Blazor WebAssembly project (client-side interactivity)

tests/
└── EntKube.Web.Tests/     # xUnit test project
```

New features typically touch a service in `Services/`, a page under `Components/Pages/`, and occasionally a migration in `Data/Migrations/`.

## Running the App

```bash
dotnet run --project src/EntKube.Web
```

The app starts at `https://localhost:7001`. Hot reload is available:

```bash
dotnet watch --project src/EntKube.Web
```

## Running Tests

```bash
dotnet test tests/EntKube.Web.Tests
```

## Making Changes

### Adding a database migration

```bash
dotnet ef migrations add <MigrationName> \
  --project src/EntKube.Web \
  --context ApplicationDbContext
```

Review the generated migration before committing — EF Core sometimes generates destructive operations that need to be adjusted.

### Adding a new service

1. Create `src/EntKube.Web/Services/YourService.cs`.
2. Register it in `Program.cs` (or the relevant `IServiceCollection` extension).
3. Inject it into the Razor page or component that needs it.

### Adding a new page

1. Create a `.razor` file under the appropriate `Components/Pages/` subdirectory.
2. Add an `@page "/your-route"` directive.
3. Add authorization with `@attribute [Authorize]` (or a policy) as appropriate.

## Submitting a Pull Request

1. **Fork** the repository and create a branch from `main`:
   ```bash
   git checkout -b feature/your-feature-name
   ```

2. **Keep commits focused.** One logical change per commit with a clear message explaining *why*, not just what.

3. **Test your changes.** Run `dotnet test` and verify the UI works end-to-end for your change.

4. **Open a PR** against `main`. In the description:
   - Summarize what changed and why.
   - Note any migration steps or configuration changes needed.
   - Link to any related issues.

5. A maintainer will review your PR. Please respond to feedback promptly.

### What we look for in PRs

- No new compiler warnings.
- No secrets, credentials, or `.env` files committed.
- Migrations are reviewed and safe (no accidental data loss).
- UI changes have been tested in the browser, not just compiled.

## License

By contributing, you agree that your contributions will be licensed under the [GNU Affero General Public License v3.0 or later](LICENSE). This is a copyleft license — modifications to EntKube that are run as a network service must be made available to users of that service.
