"""All SQL lives here. The API layer only calls these methods, which makes
the endpoints easy to test with a fake repository."""

from __future__ import annotations

import os
from datetime import datetime, timedelta, timezone

from sqlalchemy import create_engine, text
from sqlalchemy.engine import Engine

from .status import LOW_BATTERY_PCT, OFFLINE_AFTER, as_utc, bucket_seconds, vehicle_status


def make_engine() -> Engine:
    url = os.getenv("DATABASE_URL", "mysql+pymysql://fleet:fleet@localhost:3306/fleet")
    return create_engine(
        url,
        pool_size=10,
        max_overflow=5,
        pool_pre_ping=True,  # transparently replace dropped connections
        pool_recycle=1800,
        connect_args={"init_command": "SET time_zone = '+00:00'"},
    )


class FleetRepository:
    def __init__(self, engine: Engine):
        self.engine = engine

    def ping(self) -> bool:
        with self.engine.connect() as c:
            return c.execute(text("SELECT 1")).scalar() == 1

    def summary(self) -> dict:
        now = datetime.now(timezone.utc)
        online_since = (now - OFFLINE_AFTER).replace(tzinfo=None)
        minute_ago = (now - timedelta(minutes=1)).replace(tzinfo=None)
        with self.engine.connect() as c:
            fleet = c.execute(text("""
                SELECT COUNT(*)                                             AS total,
                       COALESCE(SUM(ts >= :online_since), 0)                AS online,
                       AVG(CASE WHEN ts >= :online_since THEN battery_pct END) AS avg_battery,
                       COALESCE(SUM(ts >= :online_since AND battery_pct < :low), 0) AS low_battery,
                       COALESCE(SUM(ts >= :online_since AND speed_kph > 1), 0) AS driving
                FROM vehicle_latest
            """), {"online_since": online_since, "low": LOW_BATTERY_PCT}).mappings().one()
            open_alerts = c.execute(text("SELECT COUNT(*) FROM alerts WHERE acknowledged = FALSE")).scalar()
            # idx_ts + partition pruning keep this cheap even on a big table.
            last_minute = c.execute(
                text("SELECT COUNT(*) FROM telemetry WHERE ts >= :since"), {"since": minute_ago}
            ).scalar()
        return {
            "total_vehicles": int(fleet["total"]),
            "online_vehicles": int(fleet["online"]),
            "driving_vehicles": int(fleet["driving"]),
            "avg_battery_pct": round(float(fleet["avg_battery"]), 1) if fleet["avg_battery"] is not None else None,
            "low_battery_vehicles": int(fleet["low_battery"]),
            "open_alerts": int(open_alerts),
            "readings_last_minute": int(last_minute),
            "generated_at": now,
        }

    def vehicles(self) -> list[dict]:
        now = datetime.now(timezone.utc)
        with self.engine.connect() as c:
            rows = c.execute(text("""
                SELECT v.vehicle_id, v.ts, v.speed_kph, v.battery_pct, v.temp_c, v.lat, v.lng,
                       COALESCE(a.open_alerts, 0) AS open_alerts
                FROM vehicle_latest v
                LEFT JOIN (
                    SELECT vehicle_id, COUNT(*) AS open_alerts
                    FROM alerts WHERE acknowledged = FALSE GROUP BY vehicle_id
                ) a ON a.vehicle_id = v.vehicle_id
                ORDER BY v.vehicle_id
            """)).mappings().all()
        return [
            {**dict(r), "ts": as_utc(r["ts"]), "status": vehicle_status(r["ts"], r["speed_kph"], now)}
            for r in rows
        ]

    def vehicle(self, vehicle_id: str) -> dict | None:
        return next((v for v in self.vehicles() if v["vehicle_id"] == vehicle_id), None)

    def history(self, vehicle_id: str, minutes: int, max_points: int) -> list[dict]:
        """Readings for one vehicle, averaged into time buckets so the chart
        never receives more than max_points rows."""
        bucket = bucket_seconds(minutes, max_points)
        since = (datetime.now(timezone.utc) - timedelta(minutes=minutes)).replace(tzinfo=None)
        with self.engine.connect() as c:
            rows = c.execute(text("""
                SELECT FROM_UNIXTIME(b * :bucket) AS ts,
                       AVG(speed_kph)   AS speed_kph,
                       AVG(battery_pct) AS battery_pct,
                       AVG(temp_c)      AS temp_c
                FROM (
                    SELECT FLOOR(UNIX_TIMESTAMP(ts) / :bucket) AS b,
                           speed_kph, battery_pct, temp_c
                    FROM telemetry
                    WHERE vehicle_id = :vid AND ts >= :since
                ) t
                GROUP BY b
                ORDER BY b
            """), {"vid": vehicle_id, "since": since, "bucket": bucket}).mappings().all()
        return [
            {
                "ts": as_utc(r["ts"]),
                "speed_kph": round(float(r["speed_kph"]), 2),
                "battery_pct": round(float(r["battery_pct"]), 2),
                "temp_c": round(float(r["temp_c"]), 2),
            }
            for r in rows
        ]

    def alerts(self, open_only: bool, limit: int, vehicle_id: str | None = None) -> list[dict]:
        where, params = [], {"limit": limit}
        if open_only:
            where.append("acknowledged = FALSE")
        if vehicle_id:
            where.append("vehicle_id = :vid")
            params["vid"] = vehicle_id
        sql = "SELECT id, vehicle_id, ts, alert_type, value, message, acknowledged FROM alerts"
        if where:
            sql += " WHERE " + " AND ".join(where)
        sql += " ORDER BY ts DESC LIMIT :limit"
        with self.engine.connect() as c:
            rows = c.execute(text(sql), params).mappings().all()
        return [{**dict(r), "ts": as_utc(r["ts"]), "acknowledged": bool(r["acknowledged"])} for r in rows]

    def acknowledge(self, alert_id: int) -> bool:
        with self.engine.begin() as c:
            res = c.execute(text("UPDATE alerts SET acknowledged = TRUE WHERE id = :id"), {"id": alert_id})
            if res.rowcount:
                return True
            # rowcount is 0 both for "missing" and "already acknowledged".
            return c.execute(text("SELECT 1 FROM alerts WHERE id = :id"), {"id": alert_id}).first() is not None
