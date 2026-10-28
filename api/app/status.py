"""Small pure helpers, kept separate so they are easy to unit test."""

from __future__ import annotations

from datetime import datetime, timedelta, timezone

OFFLINE_AFTER = timedelta(seconds=30)
LOW_BATTERY_PCT = 20.0


def as_utc(ts: datetime) -> datetime:
    """MySQL returns naive datetimes; we store everything in UTC."""
    return ts.replace(tzinfo=timezone.utc) if ts.tzinfo is None else ts.astimezone(timezone.utc)


def vehicle_status(last_seen: datetime, speed_kph: float, now: datetime) -> str:
    """offline if we have not heard from the vehicle recently, otherwise
    driving or parked based on speed."""
    if now - as_utc(last_seen) > OFFLINE_AFTER:
        return "offline"
    return "driving" if speed_kph > 1.0 else "parked"


def bucket_seconds(minutes: int, max_points: int) -> int:
    """How many seconds each chart point should cover so a history query
    returns at most max_points rows (server-side downsampling)."""
    return max(1, -(-minutes * 60 // max_points))  # ceiling division
