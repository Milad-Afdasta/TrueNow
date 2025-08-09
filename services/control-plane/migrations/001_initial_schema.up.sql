-- Initial schema for Control Plane
-- This creates all the core tables needed for the real-time analytics platform

-- Enable UUID extension
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

-- Namespaces table (multi-tenant isolation)
CREATE TABLE IF NOT EXISTS namespaces (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name VARCHAR(255) UNIQUE NOT NULL,
    display_name VARCHAR(255),
    owner VARCHAR(255) NOT NULL,
    quotas JSONB NOT NULL DEFAULT '{}',
    metadata JSONB DEFAULT '{}',
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_namespaces_name ON namespaces(name);
CREATE INDEX IF NOT EXISTS idx_namespaces_owner ON namespaces(owner);

-- Tables table (logical tables within namespaces)
CREATE TABLE IF NOT EXISTS tables (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    namespace_id UUID NOT NULL REFERENCES namespaces(id) ON DELETE CASCADE,
    name VARCHAR(255) NOT NULL,
    active_version INTEGER NOT NULL DEFAULT 1,
    description TEXT,
    metadata JSONB DEFAULT '{}',
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
    UNIQUE(namespace_id, name)
);

CREATE INDEX IF NOT EXISTS idx_tables_namespace_id ON tables(namespace_id);
CREATE INDEX IF NOT EXISTS idx_tables_name ON tables(name);

-- Table versions (schema versions for tables)
CREATE TABLE IF NOT EXISTS table_versions (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    table_id UUID NOT NULL REFERENCES tables(id) ON DELETE CASCADE,
    version INTEGER NOT NULL,
    schema_json JSONB NOT NULL,
    transform_version INTEGER NOT NULL DEFAULT 1,
    hash_key_formula TEXT,
    retention_hot_hours INTEGER NOT NULL DEFAULT 24,
    allowed_lateness_ms BIGINT DEFAULT 900000,  -- 15 minutes
    correction_window_ms BIGINT DEFAULT 600000,  -- 10 minutes
    status VARCHAR(50) NOT NULL DEFAULT 'pending',
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
    UNIQUE(table_id, version),
    CONSTRAINT status_check CHECK (status IN ('pending', 'active', 'deprecated', 'archived'))
);

CREATE INDEX IF NOT EXISTS idx_table_versions_table_id ON table_versions(table_id);
CREATE INDEX IF NOT EXISTS idx_table_versions_status ON table_versions(status);

-- Epochs table (for atomic cutovers and ownership changes)
CREATE TABLE IF NOT EXISTS epochs (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    table_version_id UUID NOT NULL REFERENCES table_versions(id) ON DELETE CASCADE,
    epoch_number BIGINT NOT NULL,
    reason TEXT,
    metadata JSONB DEFAULT '{}',
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    UNIQUE(table_version_id, epoch_number)
);

CREATE INDEX IF NOT EXISTS idx_epochs_table_version_id ON epochs(table_version_id);
CREATE INDEX IF NOT EXISTS idx_epochs_epoch_number ON epochs(epoch_number);

-- Shards table (virtual shard assignments)
CREATE TABLE IF NOT EXISTS shards (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    table_version_id UUID NOT NULL REFERENCES table_versions(id) ON DELETE CASCADE,
    virtual_shard_id INTEGER NOT NULL,
    leader_node_id VARCHAR(255) NOT NULL,
    follower_node_ids TEXT[] DEFAULT '{}',
    epoch_number BIGINT NOT NULL,
    memory_budget_mb INTEGER NOT NULL DEFAULT 1024,
    status VARCHAR(50) NOT NULL DEFAULT 'active',
    metadata JSONB DEFAULT '{}',
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
    UNIQUE(table_version_id, virtual_shard_id),
    CONSTRAINT shard_status_check CHECK (status IN ('active', 'splitting', 'merging', 'draining', 'inactive'))
);

CREATE INDEX IF NOT EXISTS idx_shards_table_version_id ON shards(table_version_id);
CREATE INDEX IF NOT EXISTS idx_shards_leader_node_id ON shards(leader_node_id);
CREATE INDEX IF NOT EXISTS idx_shards_status ON shards(status);

-- API Keys table (for authentication)
CREATE TABLE IF NOT EXISTS api_keys (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    namespace_id UUID NOT NULL REFERENCES namespaces(id) ON DELETE CASCADE,
    key_hash VARCHAR(255) UNIQUE NOT NULL,
    name VARCHAR(255),
    scopes TEXT[] DEFAULT '{}',
    expires_at TIMESTAMP,
    last_used_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_api_keys_namespace_id ON api_keys(namespace_id);
CREATE INDEX IF NOT EXISTS idx_api_keys_key_hash ON api_keys(key_hash);
CREATE INDEX IF NOT EXISTS idx_api_keys_expires_at ON api_keys(expires_at);

-- Audit logs table (immutable audit trail)
CREATE TABLE IF NOT EXISTS audits (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    actor VARCHAR(255) NOT NULL,
    action VARCHAR(100) NOT NULL,
    resource_type VARCHAR(100) NOT NULL,
    resource_id VARCHAR(255),
    namespace_id UUID REFERENCES namespaces(id) ON DELETE SET NULL,
    before_value JSONB,
    after_value JSONB,
    metadata JSONB DEFAULT '{}',
    ip_address INET,
    user_agent TEXT,
    created_at TIMESTAMP NOT NULL DEFAULT NOW()
);

-- Audit table is append-only, no updates allowed
CREATE INDEX IF NOT EXISTS idx_audits_actor ON audits(actor);
CREATE INDEX IF NOT EXISTS idx_audits_action ON audits(action);
CREATE INDEX IF NOT EXISTS idx_audits_resource ON audits(resource_type, resource_id);
CREATE INDEX IF NOT EXISTS idx_audits_namespace_id ON audits(namespace_id);
CREATE INDEX IF NOT EXISTS idx_audits_created_at ON audits(created_at DESC);

-- Quotas table (per-namespace resource limits)
CREATE TABLE IF NOT EXISTS quotas (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    namespace_id UUID NOT NULL REFERENCES namespaces(id) ON DELETE CASCADE,
    max_events_per_second BIGINT DEFAULT 100000,
    max_tables INTEGER DEFAULT 100,
    max_memory_gb INTEGER DEFAULT 100,
    max_query_concurrency INTEGER DEFAULT 50,
    max_retention_days INTEGER DEFAULT 30,
    metadata JSONB DEFAULT '{}',
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
    UNIQUE(namespace_id)
);

CREATE INDEX IF NOT EXISTS idx_quotas_namespace_id ON quotas(namespace_id);

-- Node registry table (track hot-tier nodes)
CREATE TABLE IF NOT EXISTS nodes (
    id VARCHAR(255) PRIMARY KEY,
    hostname VARCHAR(255) NOT NULL,
    ip_address INET NOT NULL,
    port INTEGER NOT NULL,
    node_type VARCHAR(50) NOT NULL,
    capacity JSONB NOT NULL DEFAULT '{}',
    status VARCHAR(50) NOT NULL DEFAULT 'active',
    last_heartbeat TIMESTAMP NOT NULL DEFAULT NOW(),
    metadata JSONB DEFAULT '{}',
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
    CONSTRAINT node_type_check CHECK (node_type IN ('hot_tier', 'ingester', 'query', 'control')),
    CONSTRAINT node_status_check CHECK (status IN ('active', 'draining', 'inactive', 'failed'))
);

CREATE INDEX IF NOT EXISTS idx_nodes_node_type ON nodes(node_type);
CREATE INDEX IF NOT EXISTS idx_nodes_status ON nodes(status);
CREATE INDEX IF NOT EXISTS idx_nodes_last_heartbeat ON nodes(last_heartbeat);

-- Create updated_at trigger function
CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ language 'plpgsql';

-- Add updated_at triggers to all tables that have it
DROP TRIGGER IF EXISTS update_namespaces_updated_at ON namespaces;
CREATE TRIGGER update_namespaces_updated_at BEFORE UPDATE ON namespaces
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

DROP TRIGGER IF EXISTS update_tables_updated_at ON tables;
CREATE TRIGGER update_tables_updated_at BEFORE UPDATE ON tables
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

DROP TRIGGER IF EXISTS update_table_versions_updated_at ON table_versions;
CREATE TRIGGER update_table_versions_updated_at BEFORE UPDATE ON table_versions
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

DROP TRIGGER IF EXISTS update_shards_updated_at ON shards;
CREATE TRIGGER update_shards_updated_at BEFORE UPDATE ON shards
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

DROP TRIGGER IF EXISTS update_api_keys_updated_at ON api_keys;
CREATE TRIGGER update_api_keys_updated_at BEFORE UPDATE ON api_keys
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

DROP TRIGGER IF EXISTS update_quotas_updated_at ON quotas;
CREATE TRIGGER update_quotas_updated_at BEFORE UPDATE ON quotas
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

DROP TRIGGER IF EXISTS update_nodes_updated_at ON nodes;
CREATE TRIGGER update_nodes_updated_at BEFORE UPDATE ON nodes
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();