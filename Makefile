.PHONY: dev dev-build ha ha-build fe be test lint clean

# ── Single-node debug / dev ──────────────────────────────────────
dev:
	docker compose up

dev-build:
	docker compose up --build

dev-down:
	docker compose down

# ── Two-node HA ──────────────────────────────────────────────────
ha:
	docker compose -f docker-compose.ha.yml up

ha-build:
	docker compose -f docker-compose.ha.yml up --build

ha-down:
	docker compose -f docker-compose.ha.yml down

# ── Frontend dev server (hot-reload, proxies /api → :8080) ───────
fe:
	cd frontend && npm run dev

# ── Backend only (local Go run, no Docker) ───────────────────────
be:
	cd backend && go run ./cmd/server

# ── Tests & quality ──────────────────────────────────────────────
test-be:
	cd backend && go test ./...

test-fe:
	cd frontend && npm test

lint-be:
	cd backend && go vet ./...

lint-fe:
	cd frontend && npm run typecheck

# ── Build artifacts ───────────────────────────────────────────────
build-fe:
	cd frontend && npm run build

build-be:
	cd backend && go build -o bin/server ./cmd/server

build: build-fe build-be

# ── Cleanup ───────────────────────────────────────────────────────
clean:
	rm -rf backend/bin backend/static
	docker compose down -v
