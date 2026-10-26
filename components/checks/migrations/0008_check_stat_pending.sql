-- A failed beat that leaves its Check pending (not yet down) counts in
-- pending, left out of uptime like maint.
ALTER TABLE check_stat ADD COLUMN pending INTEGER NOT NULL DEFAULT 0;

-- Buckets from before this counted those beats in down. check_event still
-- holds the beats of the last 24 h (and older ones that changed the
-- state), each with the state it left, so those beats move from down to
-- pending in their minute, hour and (UTC) day buckets. The swept ones
-- can't be told apart: older hour and day buckets keep them in down.
CREATE TEMP TABLE pending_beats AS
SELECT e.check_id, p.period, e.at - e.at % p.size AS bucket, COUNT(*) AS n
FROM check_event e
CROSS JOIN (SELECT 'minute' AS period, 60000 AS size
            UNION ALL SELECT 'hour', 3600000
            UNION ALL SELECT 'day', 86400000) p
WHERE NOT e.up AND NOT e.maint AND e.state = 'pending'
GROUP BY e.check_id, p.period, bucket;

-- SET reads the row before the update, so both see the old down. MIN
-- keeps down from going below 0 for a beat older than its bucket's count.
UPDATE check_stat SET
  pending = (SELECT MIN(b.n, check_stat.down) FROM pending_beats b
             WHERE b.check_id = check_stat.check_id AND b.period = check_stat.period AND b.bucket = check_stat.bucket),
  down = down - (SELECT MIN(b.n, check_stat.down) FROM pending_beats b
                 WHERE b.check_id = check_stat.check_id AND b.period = check_stat.period AND b.bucket = check_stat.bucket)
WHERE EXISTS (SELECT 1 FROM pending_beats b
              WHERE b.check_id = check_stat.check_id AND b.period = check_stat.period AND b.bucket = check_stat.bucket);

DROP TABLE pending_beats;
