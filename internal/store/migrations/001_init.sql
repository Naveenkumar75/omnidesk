CREATE TABLE IF NOT EXISTS tenants (
    id         SERIAL PRIMARY KEY,
    name       VARCHAR(255) NOT NULL,
    domain     VARCHAR(255) UNIQUE NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS users (
    id            SERIAL PRIMARY KEY,
    tenant_id     INT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    email         VARCHAR(255) UNIQUE NOT NULL,
    role          VARCHAR(50) NOT NULL,
    password_hash VARCHAR(255) NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_users_tenant ON users(tenant_id);
