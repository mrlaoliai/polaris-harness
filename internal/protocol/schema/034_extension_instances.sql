-- 034_extension_instances.sql
-- 统一安装实例表。所有已安装扩展的单一事实来源（SSoT）。
-- 替代 skill_sources(023) / plugins(027) / apps(028) 三表散乱记录。
-- 详见 docs/arch/M13-bis-Extension-Registry.md § 1 三层模型。

CREATE TABLE IF NOT EXISTS extension_instances (
    id           TEXT PRIMARY KEY,          -- "ext_{8字节hex}"
    ext_type     TEXT NOT NULL,             -- 'mcp' | 'skill' | 'plugin' | 'app'
    origin       TEXT NOT NULL,             -- 'builtin' | 'marketplace' | 'user' | 'learned'
    catalog_id   TEXT NOT NULL DEFAULT '',  -- 关联 registry_cache.id；user/learned 时为空
    name         TEXT NOT NULL,
    publisher    TEXT NOT NULL DEFAULT '',
    trust_tier   INTEGER NOT NULL DEFAULT 0,
    enabled      INTEGER NOT NULL DEFAULT 1,
    runtime_id   TEXT NOT NULL DEFAULT '',  -- mcp_servers.id 或 skills.name；安装完成后写入
    install_path TEXT NOT NULL DEFAULT '',  -- 文件系统绝对路径；MCP/App 为空字符串
    config       TEXT NOT NULL DEFAULT '{}', -- JSON：覆盖参数（env/args/entrypoint）
    status       TEXT NOT NULL DEFAULT 'installed', -- 'downloading'|'installed'|'error'|'disabled'
    error_msg    TEXT NOT NULL DEFAULT '',
    parent_id    TEXT NOT NULL DEFAULT '',  -- plugin bundle 子记录指向父 extension_instances.id
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
    updated_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);

CREATE INDEX IF NOT EXISTS idx_ext_type    ON extension_instances(ext_type);
CREATE INDEX IF NOT EXISTS idx_ext_origin  ON extension_instances(origin);
CREATE INDEX IF NOT EXISTS idx_ext_catalog ON extension_instances(catalog_id) WHERE catalog_id != '';
CREATE INDEX IF NOT EXISTS idx_ext_status  ON extension_instances(status);
CREATE INDEX IF NOT EXISTS idx_ext_parent  ON extension_instances(parent_id) WHERE parent_id != '';
CREATE UNIQUE INDEX IF NOT EXISTS uniq_ext_catalog
    ON extension_instances(catalog_id) WHERE catalog_id != '' AND parent_id = '';
