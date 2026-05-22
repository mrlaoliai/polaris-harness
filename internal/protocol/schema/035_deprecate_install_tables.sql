-- 035_deprecate_install_tables.sql
-- 将 skill_sources / plugins / apps 存量数据迁移到 extension_instances。
-- 原表保留（不 DROP），避免破坏可能存在的外部引用。

-- 迁移 skill_sources → extension_instances
INSERT OR IGNORE INTO extension_instances
    (id, ext_type, origin, catalog_id, name, publisher, trust_tier, enabled,
     runtime_id, install_path, status, created_at, updated_at)
SELECT
    id,
    type,                                                       -- 'skill'|'plugin'|'app'
    CASE WHEN catalog_id != '' THEN 'marketplace' ELSE 'user' END,
    COALESCE(catalog_id, ''),
    name,
    COALESCE(publisher, ''),
    COALESCE(trust_tier, 1),
    COALESCE(enabled, 1),
    '',                                                         -- runtime_id 待安装流补写
    '',
    'installed',
    created_at,
    updated_at
FROM skill_sources;

-- 迁移 plugins → extension_instances（plugins 表无 catalog_id 列，默认 user origin）
INSERT OR IGNORE INTO extension_instances
    (id, ext_type, origin, catalog_id, name, publisher, trust_tier, enabled,
     runtime_id, install_path, status, created_at, updated_at)
SELECT
    id,
    'plugin',
    'user',
    '',
    name,
    COALESCE(publisher, ''),
    COALESCE(trust_tier, 1),
    COALESCE(enabled, 1),
    '',
    '',
    'installed',
    created_at,
    updated_at
FROM plugins;

-- 迁移 apps → extension_instances（apps 表无 catalog_id 列，默认 user origin）
INSERT OR IGNORE INTO extension_instances
    (id, ext_type, origin, catalog_id, name, publisher, trust_tier, enabled,
     runtime_id, install_path, config, status, created_at, updated_at)
SELECT
    id,
    'app',
    'user',
    '',
    name,
    COALESCE(publisher, ''),
    COALESCE(trust_tier, 1),
    COALESCE(enabled, 1),
    '',
    '',
    json_object('url', url),
    'installed',
    created_at,
    updated_at
FROM apps;
