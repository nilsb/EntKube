-- +goose Up
-- +goose StatementBegin

-- ────────────────────────────────────────────────────────────────
-- Enums (Postgres native)
-- ────────────────────────────────────────────────────────────────
CREATE TYPE sync_status AS ENUM (
    'unknown', 'synced', 'out_of_sync', 'syncing', 'failed'
);
CREATE TYPE health_status AS ENUM (
    'unknown', 'healthy', 'progressing', 'degraded', 'missing', 'suspended'
);
CREATE TYPE deployment_type AS ENUM (
    'manual', 'yaml', 'helm_chart', 'git_yaml', 'git_helm', 'git_app_of_apps'
);
CREATE TYPE incident_status AS ENUM ('active', 'acknowledged', 'resolved');
CREATE TYPE component_status AS ENUM (
    'not_installed', 'installing', 'installed', 'failed', 'uninstalling'
);
CREATE TYPE notification_channel_type AS ENUM ('slack', 'teams', 'email', 'webhook');
CREATE TYPE severity_filter AS ENUM ('all', 'warning_and_above', 'critical_only');
CREATE TYPE git_auth_type AS ENUM ('none', 'https_pat', 'https_password', 'ssh_key');
CREATE TYPE tls_mode AS ENUM ('cluster_issuer', 'manual');

-- ────────────────────────────────────────────────────────────────
-- Tenant-level groupings
-- ────────────────────────────────────────────────────────────────
CREATE TABLE environments (
    id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id      UUID        NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name           TEXT        NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    origin_node_id UUID        NOT NULL REFERENCES ha_nodes(id),
    UNIQUE (tenant_id, name)
);

CREATE TABLE customers (
    id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id      UUID        NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name           TEXT        NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    origin_node_id UUID        NOT NULL REFERENCES ha_nodes(id),
    UNIQUE (tenant_id, name)
);

-- ────────────────────────────────────────────────────────────────
-- Git credential store
-- ────────────────────────────────────────────────────────────────
CREATE TABLE git_repositories (
    id                         UUID          PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id                  UUID          NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name                       TEXT          NOT NULL,
    url                        TEXT          NOT NULL,
    auth_type                  git_auth_type NOT NULL DEFAULT 'none',
    username                   TEXT,
    default_branch             TEXT          NOT NULL DEFAULT 'main',
    -- Credentials stored encrypted in the tenant vault; this is the vault key name
    credential_vault_key       TEXT,
    created_at                 TIMESTAMPTZ   NOT NULL DEFAULT now(),
    updated_at                 TIMESTAMPTZ   NOT NULL DEFAULT now(),
    origin_node_id             UUID          NOT NULL REFERENCES ha_nodes(id),
    UNIQUE (tenant_id, name)
);

-- ────────────────────────────────────────────────────────────────
-- Kubernetes clusters
-- ────────────────────────────────────────────────────────────────
CREATE TABLE kubernetes_clusters (
    id                UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id         UUID        NOT NULL REFERENCES tenants(id)      ON DELETE CASCADE,
    environment_id    UUID        NOT NULL REFERENCES environments(id)  ON DELETE RESTRICT,
    name              TEXT        NOT NULL,
    api_server_url    TEXT        NOT NULL,
    -- kubeconfig stored as vault secret key name (actual YAML in vault)
    kubeconfig_vault_key TEXT,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    origin_node_id    UUID        NOT NULL REFERENCES ha_nodes(id),
    UNIQUE (tenant_id, name)
);

-- Cluster-scoped components (Prometheus, cert-manager, Harbor, etc.)
CREATE TABLE cluster_components (
    id               UUID             PRIMARY KEY DEFAULT gen_random_uuid(),
    cluster_id       UUID             NOT NULL REFERENCES kubernetes_clusters(id) ON DELETE CASCADE,
    name             TEXT             NOT NULL,
    helm_chart_name  TEXT,
    helm_repo_url    TEXT,
    helm_chart_version TEXT,
    release_name     TEXT,
    helm_values      TEXT,
    namespace        TEXT,
    status           component_status NOT NULL DEFAULT 'not_installed',
    last_error       TEXT,
    installed_at     TIMESTAMPTZ,
    created_at       TIMESTAMPTZ      NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ      NOT NULL DEFAULT now(),
    origin_node_id   UUID             NOT NULL REFERENCES ha_nodes(id),
    UNIQUE (cluster_id, name)
);

-- ────────────────────────────────────────────────────────────────
-- Applications
-- ────────────────────────────────────────────────────────────────
CREATE TABLE apps (
    id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    customer_id    UUID        NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
    name           TEXT        NOT NULL,
    namespace      TEXT,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    origin_node_id UUID        NOT NULL REFERENCES ha_nodes(id),
    UNIQUE (customer_id, name)
);

-- Many-to-many: app ↔ environment (assigns app to environments)
CREATE TABLE app_environments (
    app_id         UUID        NOT NULL REFERENCES apps(id)         ON DELETE CASCADE,
    environment_id UUID        NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    namespace      TEXT,
    linked_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    origin_node_id UUID        NOT NULL REFERENCES ha_nodes(id),
    PRIMARY KEY (app_id, environment_id)
);

-- ────────────────────────────────────────────────────────────────
-- Deployments
-- ────────────────────────────────────────────────────────────────
CREATE TABLE app_deployments (
    id                     UUID            PRIMARY KEY DEFAULT gen_random_uuid(),
    app_id                 UUID            NOT NULL REFERENCES apps(id)                 ON DELETE CASCADE,
    environment_id         UUID            NOT NULL REFERENCES environments(id)          ON DELETE RESTRICT,
    cluster_id             UUID            NOT NULL REFERENCES kubernetes_clusters(id)   ON DELETE RESTRICT,
    name                   TEXT            NOT NULL,
    deployment_type        deployment_type NOT NULL DEFAULT 'manual',
    namespace              TEXT            NOT NULL,

    -- Sync / health state (updated by DeploymentSyncService)
    sync_status            sync_status     NOT NULL DEFAULT 'unknown',
    health_status          health_status   NOT NULL DEFAULT 'unknown',
    status_message         TEXT,
    last_synced_at         TIMESTAMPTZ,

    -- Helm chart source
    helm_repo_url          TEXT,
    helm_chart_name        TEXT,
    helm_chart_version     TEXT,
    helm_values            TEXT,

    -- Git source
    git_url                TEXT,
    git_repository_id      UUID REFERENCES git_repositories(id) ON DELETE SET NULL,
    git_path               TEXT            NOT NULL DEFAULT '.',
    git_revision           TEXT            NOT NULL DEFAULT 'main',
    git_last_synced_commit TEXT,
    git_last_synced_at     TIMESTAMPTZ,
    git_auto_sync          BOOLEAN         NOT NULL DEFAULT TRUE,

    -- App-of-apps hierarchy
    parent_deployment_id   UUID REFERENCES app_deployments(id) ON DELETE SET NULL,

    created_at             TIMESTAMPTZ     NOT NULL DEFAULT now(),
    updated_at             TIMESTAMPTZ     NOT NULL DEFAULT now(),
    origin_node_id         UUID            NOT NULL REFERENCES ha_nodes(id),
    UNIQUE (app_id, name)
);

-- YAML manifests extracted from Git / user input
CREATE TABLE deployment_manifests (
    id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    deployment_id  UUID        NOT NULL REFERENCES app_deployments(id) ON DELETE CASCADE,
    kind           TEXT        NOT NULL,
    name           TEXT        NOT NULL,
    sort_order     INT         NOT NULL DEFAULT 0,
    yaml_content   TEXT        NOT NULL,
    source_file    TEXT,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Live Kubernetes resource tree (ArgoCD resource tree style)
CREATE TABLE deployment_resources (
    id               UUID          PRIMARY KEY DEFAULT gen_random_uuid(),
    deployment_id    UUID          NOT NULL REFERENCES app_deployments(id) ON DELETE CASCADE,
    api_group        TEXT          NOT NULL DEFAULT '',
    api_version      TEXT          NOT NULL,
    kind             TEXT          NOT NULL,
    name             TEXT          NOT NULL,
    namespace        TEXT,
    sync_status      sync_status   NOT NULL DEFAULT 'unknown',
    health_status    health_status NOT NULL DEFAULT 'unknown',
    status_message   TEXT,
    parent_resource_id UUID REFERENCES deployment_resources(id) ON DELETE CASCADE,
    created_at       TIMESTAMPTZ   NOT NULL DEFAULT now(),
    last_updated_at  TIMESTAMPTZ   NOT NULL DEFAULT now()
);
CREATE INDEX deployment_resources_deployment ON deployment_resources(deployment_id);

-- Periodic health snapshots (kept 90 days by UptimeTrackingService)
CREATE TABLE deployment_health_snapshots (
    id             UUID          PRIMARY KEY DEFAULT gen_random_uuid(),
    deployment_id  UUID          NOT NULL REFERENCES app_deployments(id) ON DELETE CASCADE,
    health_status  health_status NOT NULL,
    sync_status    sync_status   NOT NULL,
    ready_replicas INT,
    total_replicas INT,
    snapshot_at    TIMESTAMPTZ   NOT NULL DEFAULT now()
);
CREATE INDEX deployment_health_snapshots_deployment ON deployment_health_snapshots(deployment_id, snapshot_at DESC);

-- ────────────────────────────────────────────────────────────────
-- External routes (Ingress / Gateway with health probing)
-- ────────────────────────────────────────────────────────────────
CREATE TABLE external_routes (
    id                UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    component_id      UUID        NOT NULL REFERENCES cluster_components(id) ON DELETE CASCADE,
    hostname          TEXT        NOT NULL,
    service_name      TEXT,
    service_port      INT         NOT NULL DEFAULT 80,
    path_prefix       TEXT        NOT NULL DEFAULT '/',
    tls_mode          tls_mode    NOT NULL DEFAULT 'cluster_issuer',
    cluster_issuer_name TEXT,
    -- Health monitoring (updated by ExternalRouteHealthService)
    last_health_check_at TIMESTAMPTZ,
    last_status_code  INT,
    is_reachable      BOOLEAN,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    origin_node_id    UUID        NOT NULL REFERENCES ha_nodes(id)
);

-- Route health history (kept 90 days)
CREATE TABLE external_route_health_history (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    route_id     UUID        NOT NULL REFERENCES external_routes(id) ON DELETE CASCADE,
    is_reachable BOOLEAN     NOT NULL,
    status_code  INT,
    response_ms  INT,
    checked_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX route_health_history_route ON external_route_health_history(route_id, checked_at DESC);

-- ────────────────────────────────────────────────────────────────
-- Alert management
-- ────────────────────────────────────────────────────────────────
CREATE TABLE notification_channels (
    id                UUID                     PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id         UUID                     NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name              TEXT                     NOT NULL,
    channel_type      notification_channel_type NOT NULL,
    configuration_json TEXT                    NOT NULL DEFAULT '{}',
    is_enabled        BOOLEAN                  NOT NULL DEFAULT TRUE,
    severity_filter   severity_filter          NOT NULL DEFAULT 'all',
    created_at        TIMESTAMPTZ              NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ              NOT NULL DEFAULT now(),
    origin_node_id    UUID                     NOT NULL REFERENCES ha_nodes(id)
);

CREATE TABLE maintenance_windows (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   UUID        NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name        TEXT        NOT NULL,
    starts_at   TIMESTAMPTZ NOT NULL,
    ends_at     TIMESTAMPTZ NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    origin_node_id UUID     NOT NULL REFERENCES ha_nodes(id)
);

CREATE TABLE alert_routing_rules (
    id                  UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id           UUID        NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name                TEXT        NOT NULL,
    priority            INT         NOT NULL DEFAULT 100,
    channel_id          UUID        REFERENCES notification_channels(id) ON DELETE SET NULL,
    match_alert_name    TEXT,
    match_namespace     TEXT,
    match_severity      TEXT,
    match_label_key     TEXT,
    match_label_value   TEXT,
    match_cluster_id    UUID        REFERENCES kubernetes_clusters(id) ON DELETE SET NULL,
    is_enabled          BOOLEAN     NOT NULL DEFAULT TRUE,
    suppress_incident   BOOLEAN     NOT NULL DEFAULT FALSE,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    origin_node_id      UUID        NOT NULL REFERENCES ha_nodes(id)
);

CREATE TABLE alert_incidents (
    id               UUID            PRIMARY KEY DEFAULT gen_random_uuid(),
    cluster_id       UUID            NOT NULL REFERENCES kubernetes_clusters(id) ON DELETE CASCADE,
    fingerprint      TEXT            NOT NULL,
    alert_name       TEXT            NOT NULL,
    severity         TEXT            NOT NULL,
    summary          TEXT            NOT NULL DEFAULT '',
    description      TEXT            NOT NULL DEFAULT '',
    runbook_url      TEXT            NOT NULL DEFAULT '',
    labels_json      TEXT            NOT NULL DEFAULT '{}',
    starts_at        TIMESTAMPTZ     NOT NULL,
    ends_at          TIMESTAMPTZ,
    status           incident_status NOT NULL DEFAULT 'active',
    acknowledged_by  TEXT,
    acknowledged_at  TIMESTAMPTZ,
    resolved_at      TIMESTAMPTZ,
    assigned_to      TEXT,
    assigned_at      TIMESTAMPTZ,
    escalated_at     TIMESTAMPTZ,
    created_at       TIMESTAMPTZ     NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ     NOT NULL DEFAULT now(),
    UNIQUE (cluster_id, fingerprint)
);
CREATE INDEX alert_incidents_status ON alert_incidents(status, cluster_id);

CREATE TABLE incident_notes (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    incident_id UUID        NOT NULL REFERENCES alert_incidents(id) ON DELETE CASCADE,
    author      TEXT        NOT NULL DEFAULT 'system',
    body        TEXT        NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE notification_deliveries (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    incident_id UUID        NOT NULL REFERENCES alert_incidents(id) ON DELETE CASCADE,
    channel_id  UUID        REFERENCES notification_channels(id) ON DELETE SET NULL,
    success     BOOLEAN     NOT NULL,
    error_msg   TEXT,
    sent_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS notification_deliveries;
DROP TABLE IF EXISTS incident_notes;
DROP TABLE IF EXISTS alert_incidents;
DROP TABLE IF EXISTS alert_routing_rules;
DROP TABLE IF EXISTS maintenance_windows;
DROP TABLE IF EXISTS notification_channels;
DROP TABLE IF EXISTS external_route_health_history;
DROP TABLE IF EXISTS external_routes;
DROP TABLE IF EXISTS deployment_health_snapshots;
DROP TABLE IF EXISTS deployment_resources;
DROP TABLE IF EXISTS deployment_manifests;
DROP TABLE IF EXISTS app_deployments;
DROP TABLE IF EXISTS app_environments;
DROP TABLE IF EXISTS apps;
DROP TABLE IF EXISTS cluster_components;
DROP TABLE IF EXISTS kubernetes_clusters;
DROP TABLE IF EXISTS git_repositories;
DROP TABLE IF EXISTS customers;
DROP TABLE IF EXISTS environments;
DROP TYPE IF EXISTS tls_mode;
DROP TYPE IF EXISTS git_auth_type;
DROP TYPE IF EXISTS severity_filter;
DROP TYPE IF EXISTS notification_channel_type;
DROP TYPE IF EXISTS component_status;
DROP TYPE IF EXISTS incident_status;
DROP TYPE IF EXISTS deployment_type;
DROP TYPE IF EXISTS health_status;
DROP TYPE IF EXISTS sync_status;
-- +goose StatementEnd
