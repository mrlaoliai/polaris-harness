-- ============================================================================
-- 021_plugins: 独立程序插件运行配置表
-- ============================================================================
-- 架构角色: 记录行业标准扩展包（如 Anthropic/OpenAI Plugin Bundle）的运行时入口配置。
-- Plugin 本身是一个复合容器，其内部可能挂载有 MCP Server、Skills 或独立脚本。
-- 本表记录此类插件的入口点、环境及全局设定，具体能力的执行由其内部挂载的各模块引擎负责。
-- 关联: M13-bis(Extension Registry), ADR-0019
-- ============================================================================

CREATE TABLE IF NOT EXISTS plugins (
    id          TEXT    PRIMARY KEY,
    name        TEXT    NOT NULL,
    description TEXT    NOT NULL DEFAULT '',
    entrypoint  TEXT    NOT NULL DEFAULT '',        -- 执行命令，如 "python3 main.py"
    args        TEXT    NOT NULL DEFAULT '[]',      -- 默认执行参数 (JSON array)
    env         TEXT    NOT NULL DEFAULT '{}',      -- 默认环境变量 (JSON object)
    enabled     INTEGER NOT NULL DEFAULT 1,
    timeout     INTEGER NOT NULL DEFAULT 60,        -- 执行超时（秒）
    trust_tier  INTEGER NOT NULL DEFAULT 1,         -- 信任等级 (与 taint 同步)
    catalog_id  TEXT    NOT NULL DEFAULT '',        -- 关联 extension_catalog.id
    created_at  TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
    updated_at  TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);

CREATE INDEX IF NOT EXISTS idx_plugins_enabled   ON plugins(enabled);
CREATE INDEX IF NOT EXISTS idx_plugins_catalog   ON plugins(catalog_id) WHERE catalog_id != '';
CREATE INDEX IF NOT EXISTS idx_plugins_trust     ON plugins(trust_tier);
