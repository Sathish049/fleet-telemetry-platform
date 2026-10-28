"""Vehicle fleet simulator.

Pretends to be a fleet of electric vehicles driving around Fremont, CA.
Each vehicle opens its own WebSocket to the ingest service and sends one
telemetry reading every --interval seconds.

Vehicles drive, park, and charge. A few of them randomly develop faults
(overheating or fast battery drain) so the alert rules have something to
catch. The simulator can also send duplicate and invalid messages to show
that the pipeline handles them.

Examples:
    python simulator.py --vehicles 50                   # demo
    python simulator.py --vehicles 500 --interval 0.1   # load test, ~5,000 msg/s

    # One Python process tops out around 4,000 msg/s; for more, run two:
    python simulator.py --vehicles 300 --interval 0.1 &
    python simulator.py --vehicles 300 --interval 0.1 --id-offset 300
"""

from __future__ import annotations

import argparse
import asyncio
import json
import math
import random
import signal
import time
from dataclasses import dataclass, field
from datetime import datetime, timezone

import websockets

# Area the fleet drives in (around Fremont, CA).
CENTER_LAT, CENTER_LNG = 37.5485, -121.9886
RADIUS_DEG = 0.12  # roughly 13 km

DRIVING, PARKED, CHARGING = "driving", "parked", "charging"


@dataclass
class Stats:
    sent: int = 0
    duplicates: int = 0
    invalid: int = 0
    rejected: int = 0
    reconnects: int = 0
    connected: int = 0
    started: float = field(default_factory=time.monotonic)


@dataclass
class Vehicle:
    vehicle_id: str
    lat: float
    lng: float
    heading: float
    battery: float
    speed: float = 0.0
    target_speed: float = 50.0
    temp: float = 25.0
    state: str = DRIVING
    state_until: float = 0.0
    fault: str | None = None  # "overheat" or "drain"
    fault_until: float = 0.0
    charge_below: float = 20.0

    @classmethod
    def random(cls, idx: int) -> "Vehicle":
        angle = random.uniform(0, 2 * math.pi)
        dist = RADIUS_DEG * math.sqrt(random.random())
        return cls(
            vehicle_id=f"VH-{idx:04d}",
            lat=CENTER_LAT + dist * math.sin(angle),
            lng=CENTER_LNG + dist * math.cos(angle),
            heading=random.uniform(0, 360),
            battery=random.uniform(35, 100),
            target_speed=random.uniform(30, 110),
            charge_below=random.uniform(7, 25),
        )

    def step(self, dt: float, fault_rate: float) -> None:
        """Advance the vehicle's physics by dt seconds."""
        now = time.monotonic()

        # Random faults start and end.
        if self.fault is None and random.random() < fault_rate * dt:
            self.fault = random.choice(["overheat", "drain"])
            self.fault_until = now + random.uniform(60, 180)
        elif self.fault and now > self.fault_until:
            self.fault = None

        # State machine: driving -> parked -> driving, or -> charging when low.
        if self.state == DRIVING:
            if self.battery < self.charge_below:
                self.state = CHARGING
            elif random.random() < 0.002 * dt:
                self.state, self.state_until = PARKED, now + random.uniform(20, 90)
        elif self.state == PARKED and now > self.state_until:
            self.state = DRIVING
        elif self.state == CHARGING and self.battery >= random.uniform(85, 95):
            self.state = DRIVING

        # Speed moves smoothly toward a target that changes now and then.
        if self.state == DRIVING:
            if random.random() < 0.05 * dt:
                self.target_speed = random.uniform(25, 120)
            self.speed += (self.target_speed - self.speed) * min(1.0, 0.3 * dt)
            self.heading = (self.heading + random.gauss(0, 8) * dt) % 360
        else:
            self.speed = max(0.0, self.speed - 30 * dt)

        # Move. 1 degree of latitude is about 111 km.
        km = self.speed * dt / 3600
        rad = math.radians(self.heading)
        self.lat += (km / 111) * math.cos(rad)
        self.lng += (km / (111 * math.cos(math.radians(self.lat)))) * math.sin(rad)
        if math.hypot(self.lat - CENTER_LAT, self.lng - CENTER_LNG) > RADIUS_DEG:
            # Turn back toward the center when leaving the area.
            self.heading = (math.degrees(math.atan2(CENTER_LNG - self.lng, CENTER_LAT - self.lat)) + 360) % 360

        # Battery: drains with speed, charges when charging.
        if self.state == CHARGING:
            self.battery = min(100.0, self.battery + 0.4 * dt)
        else:
            self.battery -= (0.0002 * self.speed + 0.0005) * dt
            if self.fault == "drain":
                self.battery -= 0.07 * dt  # about 21% in 5 minutes
        self.battery = max(0.0, self.battery)

        # Temperature: ambient + load + charging heat, with some noise.
        target_temp = 24 + 0.12 * self.speed + (10 if self.state == CHARGING else 0)
        if self.fault == "overheat":
            target_temp = 72
        self.temp += (target_temp - self.temp) * min(1.0, 0.05 * dt) + random.gauss(0, 0.2)

    def reading(self) -> dict:
        ts = datetime.now(timezone.utc).isoformat(timespec="milliseconds").replace("+00:00", "Z")
        return {
            "vehicle_id": self.vehicle_id,
            "ts": ts,
            "speed_kph": round(self.speed, 2),
            "battery_pct": round(self.battery, 2),
            "temp_c": round(self.temp, 2),
            "lat": round(self.lat, 6),
            "lng": round(self.lng, 6),
        }


async def read_replies(ws, stats: Stats) -> None:
    """The server only replies when it rejects a message."""
    try:
        async for _ in ws:
            stats.rejected += 1
    except websockets.ConnectionClosed:
        pass


async def run_vehicle(v: Vehicle, args, stats: Stats, stop: asyncio.Event) -> None:
    # Spread start times so 500 vehicles do not all connect in the same millisecond.
    await asyncio.sleep(random.uniform(0, min(args.interval, 1.0)))
    backoff = 0.5
    while not stop.is_set():
        try:
            async with websockets.connect(args.url, max_queue=None, ping_interval=20) as ws:
                stats.connected += 1
                backoff = 0.5
                reader = asyncio.create_task(read_replies(ws, stats))
                last = time.monotonic()
                try:
                    while not stop.is_set():
                        now = time.monotonic()
                        v.step(now - last, args.fault_rate)
                        last = now
                        msg = v.reading()

                        if random.random() < args.invalid_rate:
                            msg = {**msg, "battery_pct": 150.0}  # out of range on purpose
                            stats.invalid += 1
                        payload = json.dumps(msg)
                        await ws.send(payload)
                        stats.sent += 1
                        if random.random() < args.duplicate_rate:
                            await ws.send(payload)  # same message again
                            stats.duplicates += 1

                        await asyncio.sleep(args.interval)
                finally:
                    stats.connected -= 1
                    reader.cancel()
        except (OSError, websockets.WebSocketException):
            stats.reconnects += 1
            await asyncio.sleep(backoff)
            backoff = min(backoff * 2, 10)


async def report(stats: Stats, stop: asyncio.Event, every: float = 5.0) -> None:
    last_sent, last_t = 0, time.monotonic()
    while not stop.is_set():
        await asyncio.sleep(every)
        now = time.monotonic()
        rate = (stats.sent - last_sent) / (now - last_t)
        last_sent, last_t = stats.sent, now
        print(
            f"[sim] connected={stats.connected:4d}  sent={stats.sent:9d}  rate={rate:8.1f} msg/s  "
            f"dups={stats.duplicates}  invalid={stats.invalid}  rejected={stats.rejected}  reconnects={stats.reconnects}",
            flush=True,
        )


async def main() -> None:
    p = argparse.ArgumentParser(description="Simulate a fleet of vehicles sending telemetry.")
    p.add_argument("--url", default="ws://localhost:8080/ws", help="ingest WebSocket URL")
    p.add_argument("--vehicles", type=int, default=50)
    p.add_argument("--interval", type=float, default=1.0, help="seconds between readings per vehicle")
    p.add_argument("--duration", type=float, default=0, help="stop after N seconds (0 = run forever)")
    p.add_argument("--fault-rate", type=float, default=0.002, help="chance per vehicle per second of starting a fault")
    p.add_argument("--duplicate-rate", type=float, default=0.01, help="fraction of messages sent twice")
    p.add_argument("--invalid-rate", type=float, default=0.001, help="fraction of messages made invalid")
    p.add_argument("--id-offset", type=int, default=0,
                   help="first vehicle number minus one; use it to run several simulators without ID clashes")
    p.add_argument("--seed", type=int, default=None)
    args = p.parse_args()

    if args.seed is not None:
        random.seed(args.seed)

    stats = Stats()
    stop = asyncio.Event()
    loop = asyncio.get_running_loop()
    for sig in (signal.SIGINT, signal.SIGTERM):
        try:
            loop.add_signal_handler(sig, stop.set)
        except NotImplementedError:  # Windows
            pass

    print(f"[sim] starting {args.vehicles} vehicles -> {args.url} every {args.interval}s "
          f"(target ~{args.vehicles / args.interval:,.0f} msg/s)", flush=True)
    fleet = [Vehicle.random(args.id_offset + i + 1) for i in range(args.vehicles)]
    tasks = [asyncio.create_task(run_vehicle(v, args, stats, stop)) for v in fleet]
    tasks.append(asyncio.create_task(report(stats, stop)))

    if args.duration > 0:
        try:
            await asyncio.wait_for(stop.wait(), timeout=args.duration)
        except asyncio.TimeoutError:
            stop.set()
    else:
        await stop.wait()

    for t in tasks:
        t.cancel()
    await asyncio.gather(*tasks, return_exceptions=True)
    elapsed = time.monotonic() - stats.started
    print(f"[sim] done: sent {stats.sent} messages in {elapsed:.1f}s "
          f"(avg {stats.sent / elapsed:,.0f} msg/s), duplicates={stats.duplicates}, "
          f"invalid={stats.invalid}, rejected by server={stats.rejected}", flush=True)


if __name__ == "__main__":
    asyncio.run(main())
