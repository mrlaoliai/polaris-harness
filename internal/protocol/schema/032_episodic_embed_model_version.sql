-- ============================================================================
-- 032_episodic_embed_model_version: episodic_events 补齐 inv_M5_03
-- ============================================================================
-- inv_M5_03: embed_model_version 是一等字段——每 chunk/event 携带，
--            跨版本检索走 OnlineReindexer，DDL NOT NULL 约束。
-- 空字符串 DEFAULT 表示"未索引"，OnlineReindexer 以此为触发条件。
-- ============================================================================

ALTER TABLE episodic_events ADD COLUMN embed_model_version TEXT NOT NULL DEFAULT '';
