-- ============================================================================
-- 031_rag_lineage: rag_chunks 补齐 inv_M10_03 lineage metadata
-- ============================================================================
-- inv_M10_03: 每 chunk 携带 lineage metadata——source_uri + doc_version +
--             chunk_seq + content_hash，DDL NOT NULL 约束。
-- 已存在行用空字符串/0 作为 DEFAULT，确保 NOT NULL 语义（SQLite ALTER TABLE 限制）。
-- 向量嵌入版本字段同步补齐（inv_M5_03 embed_model_version 扩展至 rag_chunks）。
-- ============================================================================

ALTER TABLE rag_chunks ADD COLUMN source_uri        TEXT NOT NULL DEFAULT '';
ALTER TABLE rag_chunks ADD COLUMN doc_version       TEXT NOT NULL DEFAULT '';
ALTER TABLE rag_chunks ADD COLUMN chunk_seq         INTEGER NOT NULL DEFAULT 0;
ALTER TABLE rag_chunks ADD COLUMN content_hash      TEXT NOT NULL DEFAULT '';
ALTER TABLE rag_chunks ADD COLUMN embed_model_version TEXT NOT NULL DEFAULT '';
