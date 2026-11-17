-- v3: search_terms 增加 doc_id 索引。
-- 写入路径（search.indexDocTx / search.DeleteTx）按 doc_id 删除倒排词；
-- 没有该索引时 SQLite 只能 SCAN 全表，每次写入退化为 O(词条总数)，
-- 10 万条消息的 seed/重建因此变成 O(N²)（实测 1 万条 50s、2 万条 186s）。
CREATE INDEX IF NOT EXISTS idx_search_terms_doc ON search_terms(doc_id);
