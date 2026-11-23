# energy-api caches its attributed hours in its own DB

energy-api splits where a period's energy went (solar, battery and grid to the use, the battery and the export) every 5 minutes, since a split of a period's totals cancels energy that moved both ways out. A Year at 5 minutes is ~1 M counter readings, too many to read for every request. So energy-api keeps one row per **UTC hour** in its own SQLite (`Spec.Migrations`, as `apps` and `checks` do): the hour's counters and its attributed flows, the attribution's `algo` version and whether it was estimated. A period is the sum of its hours. We chose this because a past hour never changes, hours add up to a day, week, month or year in any whole-hour zone the browser asks in, and ddash stays one container with nothing beside it.

## Considered Options

- **Redis, or another cache beside ddash**: a second container to run, back up and upgrade for data InfluxDB already holds.
- **A cache per finished day**: fewer rows, but the browser's zone sets where a day begins, so each zone would need its own days.
- **Attribute every period live**: correct, but a Year is a minute of queries on every read.
- **Keep the period's buckets in memory only**: lost on every restart, and the cost of a first Year read stays.

## Consequences

- InfluxDB stays the source of truth and the cache is disposable: deleting the DB, or bumping `algoVersion`, makes the next reads attribute the hours again. A changed InfluxDB URL or bucket clears it at Start.
- Freshness: an hour attributed more than 48 h after its end is final. One attributed earlier is served for 45 s, then attributed again on the next read, so late Telegraf points count. The current hour is attributed up to now on every read and never cached.
- Missing hours are attributed a week at a time, so no single query reads more than a week of 5-minute windows, and a background warm-up after Start (and every hour) fills the cache back to the first reading. The in-memory period cache stays in front of it, holding a whole answer (with its curve) for 45 s, or an hour once the period is over.
- energy-api's counters (the period's totals) now come from the cached hours too, so the Sankey, the tiles and the ledger can't disagree. In a zone whose offset isn't whole hours (e.g. +05:30), the hour a period begins in counts whole to it, so it overlaps the period before by its offset's minutes.
- The only state energy-api writes is this cache; it still never writes to InfluxDB.
- Details: [The Sankey and self-sufficiency split the period's totals](https://github.com/itsKontra/ddash/issues/325).
