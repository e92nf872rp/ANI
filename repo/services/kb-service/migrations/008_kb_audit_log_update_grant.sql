-- Parse/reparse result-in-place update: the parse consumer flips the
-- doc.parse/doc.reparse intent row (written at task creation) when the
-- document reaches a terminal parse_status, instead of INSERTing a second
-- result row — the operation history shows one entry per operation.
-- 006 granted SELECT, INSERT only; UPDATE is required for the flip.
GRANT UPDATE ON kb_audit_log TO ani_app;
