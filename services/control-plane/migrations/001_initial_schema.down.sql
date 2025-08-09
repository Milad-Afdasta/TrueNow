-- Rollback initial schema

-- Drop triggers first
DROP TRIGGER IF EXISTS update_nodes_updated_at ON nodes;
DROP TRIGGER IF EXISTS update_quotas_updated_at ON quotas;
DROP TRIGGER IF EXISTS update_api_keys_updated_at ON api_keys;
DROP TRIGGER IF EXISTS update_shards_updated_at ON shards;
DROP TRIGGER IF EXISTS update_table_versions_updated_at ON table_versions;
DROP TRIGGER IF EXISTS update_tables_updated_at ON tables;
DROP TRIGGER IF EXISTS update_namespaces_updated_at ON namespaces;

-- Drop function
DROP FUNCTION IF EXISTS update_updated_at_column();

-- Drop tables in reverse order of dependencies
DROP TABLE IF EXISTS nodes;
DROP TABLE IF EXISTS quotas;
DROP TABLE IF EXISTS audits;
DROP TABLE IF EXISTS api_keys;
DROP TABLE IF EXISTS shards;
DROP TABLE IF EXISTS epochs;
DROP TABLE IF EXISTS table_versions;
DROP TABLE IF EXISTS tables;
DROP TABLE IF EXISTS namespaces;

-- Drop extension
DROP EXTENSION IF EXISTS "uuid-ossp";