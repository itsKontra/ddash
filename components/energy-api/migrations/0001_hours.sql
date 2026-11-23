-- The cache of attributed hours (ADR-0011). InfluxDB is the source of
-- truth: every row can be attributed again from it.

-- One row per UTC hour: its counters and where its energy went, in kWh.
-- algo is the version of the attribution; a row of another version is
-- attributed again. estimated is whether part of the hour lacked the
-- VE.Bus counters and was split by the power readings.
CREATE TABLE hour (
  at                 INTEGER PRIMARY KEY, -- Unix s, the hour's start
  algo               INTEGER NOT NULL,
  attributed_at      INTEGER NOT NULL,    -- Unix s
  estimated          INTEGER NOT NULL,
  solar              REAL    NOT NULL,
  grid_import        REAL    NOT NULL,
  grid_export        REAL    NOT NULL,
  battery_charged    REAL    NOT NULL,
  battery_discharged REAL    NOT NULL,
  solar_used         REAL    NOT NULL,
  solar_battery      REAL    NOT NULL,
  solar_export       REAL    NOT NULL,
  battery_used       REAL    NOT NULL,
  battery_export     REAL    NOT NULL,
  grid_used          REAL    NOT NULL,
  grid_battery       REAL    NOT NULL,
  charge_loss        REAL    NOT NULL,
  discharge_loss     REAL    NOT NULL,
  ac_out             REAL    NOT NULL
);

-- The InfluxDB and bucket the hours were read from: the cache is cleared
-- when either changes.
CREATE TABLE source (
  url    TEXT NOT NULL,
  bucket TEXT NOT NULL
);
