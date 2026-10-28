-- Schema for the Fleet Telemetry Platform.
-- MySQL runs this file automatically the first time the container starts.
-- All timestamps are stored in UTC.

SET time_zone = '+00:00';

-- Raw telemetry: one row per reading.
--  * PRIMARY KEY (vehicle_id, ts) makes duplicate messages impossible
--    (the writer uses INSERT IGNORE) and serves "history for one vehicle
--    over a time range" queries directly from the index.
--  * Partitioned by day. The writer service creates upcoming partitions
--    and drops ones older than RETENTION_DAYS (default 7).
CREATE TABLE IF NOT EXISTS telemetry (
    vehicle_id   VARCHAR(32)  NOT NULL,
    ts           DATETIME(3)  NOT NULL,
    speed_kph    FLOAT        NOT NULL,
    battery_pct  FLOAT        NOT NULL,
    temp_c       FLOAT        NOT NULL,
    lat          DOUBLE       NOT NULL,
    lng          DOUBLE       NOT NULL,
    received_at  DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    PRIMARY KEY (vehicle_id, ts),
    KEY idx_ts (ts)
)
PARTITION BY RANGE (TO_DAYS(ts)) (
    PARTITION pmax VALUES LESS THAN MAXVALUE
);

-- Latest known state of every vehicle (one row per vehicle).
-- The dashboard map reads this small table instead of scanning telemetry.
CREATE TABLE IF NOT EXISTS vehicle_latest (
    vehicle_id   VARCHAR(32)  NOT NULL PRIMARY KEY,
    ts           DATETIME(3)  NOT NULL,
    speed_kph    FLOAT        NOT NULL,
    battery_pct  FLOAT        NOT NULL,
    temp_c       FLOAT        NOT NULL,
    lat          DOUBLE       NOT NULL,
    lng          DOUBLE       NOT NULL,
    KEY idx_ts (ts)
);

-- Alerts produced by the rules engine in the writer service.
CREATE TABLE IF NOT EXISTS alerts (
    id            BIGINT       NOT NULL AUTO_INCREMENT PRIMARY KEY,
    vehicle_id    VARCHAR(32)  NOT NULL,
    ts            DATETIME(3)  NOT NULL,
    alert_type    VARCHAR(32)  NOT NULL,
    value         FLOAT        NOT NULL,
    message       VARCHAR(255) NOT NULL,
    acknowledged  BOOLEAN      NOT NULL DEFAULT FALSE,
    created_at    DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    KEY idx_open (acknowledged, ts),
    KEY idx_vehicle (vehicle_id, ts)
);
