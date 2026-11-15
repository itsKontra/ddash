-- Listing the Audit Log offers every target as a filter: this index answers
-- SELECT DISTINCT target without scanning the table.
CREATE INDEX audit_entry_target ON audit_entry (target);
