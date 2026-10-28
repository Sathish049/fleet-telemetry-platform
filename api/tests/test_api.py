"""API tests. A fake repository replaces MySQL, so these run anywhere."""

from datetime import datetime, timedelta, timezone

import pytest
from fastapi.testclient import TestClient

from app.main import app, get_repo
from app.status import bucket_seconds, vehicle_status

NOW = datetime(2026, 9, 27, 12, 0, 0, tzinfo=timezone.utc)


class FakeRepo:
    def __init__(self):
        self.alert_rows = [
            {"id": 1, "vehicle_id": "VH-0001", "ts": NOW, "alert_type": "HIGH_TEMP",
             "value": 66.0, "message": "Temperature 66.0°C is above 60°C", "acknowledged": False},
        ]

    def ping(self):
        return True

    def summary(self):
        return {"total_vehicles": 2, "online_vehicles": 1, "driving_vehicles": 1, "avg_battery_pct": 71.5,
                "low_battery_vehicles": 0, "open_alerts": 1, "readings_last_minute": 60, "generated_at": NOW}

    def vehicles(self):
        return [{"vehicle_id": "VH-0001", "ts": NOW, "speed_kph": 55.0, "battery_pct": 71.5, "temp_c": 66.0,
                 "lat": 37.5, "lng": -121.9, "status": "driving", "open_alerts": 1}]

    def vehicle(self, vehicle_id):
        return next((v for v in self.vehicles() if v["vehicle_id"] == vehicle_id), None)

    def history(self, vehicle_id, minutes, max_points):
        return [{"ts": NOW - timedelta(seconds=i), "speed_kph": 50.0, "battery_pct": 70.0, "temp_c": 40.0}
                for i in range(min(3, max_points))]

    def alerts(self, open_only, limit, vehicle_id=None):
        rows = [a for a in self.alert_rows if not (open_only and a["acknowledged"])]
        return rows[:limit]

    def acknowledge(self, alert_id):
        for a in self.alert_rows:
            if a["id"] == alert_id:
                a["acknowledged"] = True
                return True
        return False


@pytest.fixture
def client():
    fake = FakeRepo()
    app.dependency_overrides[get_repo] = lambda: fake
    yield TestClient(app)
    app.dependency_overrides.clear()


def test_health(client):
    assert client.get("/api/health").json() == {"status": "ok"}


def test_summary(client):
    body = client.get("/api/fleet/summary").json()
    assert body["total_vehicles"] == 2
    assert body["open_alerts"] == 1


def test_vehicle_found_and_missing(client):
    assert client.get("/api/vehicles/VH-0001").json()["status"] == "driving"
    assert client.get("/api/vehicles/NOPE").status_code == 404


def test_history_validates_query_params(client):
    assert len(client.get("/api/vehicles/VH-0001/history?minutes=15").json()) == 3
    assert client.get("/api/vehicles/VH-0001/history?minutes=0").status_code == 422


def test_acknowledge_alert_flow(client):
    assert len(client.get("/api/alerts").json()) == 1
    assert client.post("/api/alerts/1/ack").status_code == 200
    assert client.get("/api/alerts").json() == []              # no longer open
    assert len(client.get("/api/alerts?status=all").json()) == 1
    assert client.post("/api/alerts/999/ack").status_code == 404


def test_metrics_endpoint(client):
    client.get("/api/health")
    assert "api_requests_total" in client.get("/metrics").text


def test_vehicle_status():
    assert vehicle_status(NOW - timedelta(seconds=5), 40, NOW) == "driving"
    assert vehicle_status(NOW - timedelta(seconds=5), 0, NOW) == "parked"
    assert vehicle_status(NOW - timedelta(minutes=5), 40, NOW) == "offline"
    assert vehicle_status((NOW - timedelta(seconds=5)).replace(tzinfo=None), 40, NOW) == "driving"


def test_bucket_seconds():
    assert bucket_seconds(15, 300) == 3        # 900s / 300 points
    assert bucket_seconds(1, 300) == 1         # never below 1 second
    assert bucket_seconds(60, 1000) == 4       # rounds up
