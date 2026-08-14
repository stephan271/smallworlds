#!/usr/bin/env python3
"""Seed the lab Immich with real image files spread over several dates.

The pod exporter keys objects as immich/<year>/<date>/<uuid8>-<filename>, so
the seed deliberately spans multiple days and years: that is what makes the
resulting pod layout worth looking at.

Requires a port-forward to immich-server:
    kubectl port-forward -n immich svc/immich-server 2283:2283
"""
import io
import json
import mimetypes
import os
import random
import sys
import urllib.request
import uuid
from datetime import datetime, timedelta, timezone

from PIL import Image, ImageDraw

BASE = os.environ.get("IMMICH_URL", "http://127.0.0.1:2283")
EMAIL = os.environ["IMMICH_EMAIL"]
PASSWORD = os.environ["IMMICH_PASSWORD"]
COUNT = int(os.environ.get("COUNT", "20"))


def api(path, body=None, token=None, method=None):
    data = json.dumps(body).encode() if body is not None else None
    request = urllib.request.Request(
        BASE + path, data=data, method=method or ("POST" if data else "GET")
    )
    request.add_header("Content-Type", "application/json")
    if token:
        request.add_header("Authorization", "Bearer " + token)
    with urllib.request.urlopen(request, timeout=60) as response:
        return json.loads(response.read() or b"null")


def make_jpeg(index, taken):
    """A distinguishable image, so a restored copy can be told apart by eye."""
    image = Image.new("RGB", (1200, 800), tuple(random.randint(30, 200) for _ in range(3)))
    draw = ImageDraw.Draw(image)
    draw.rectangle([40, 40, 1160, 760], outline=(255, 255, 255), width=6)
    draw.text((90, 360), f"SmallWorlds lab #{index:02d}  {taken:%Y-%m-%d}", fill=(255, 255, 255))
    for _ in range(24):
        x, y = random.randint(60, 1140), random.randint(60, 740)
        draw.ellipse([x, y, x + 40, y + 40], fill=tuple(random.randint(0, 255) for _ in range(3)))
    buffer = io.BytesIO()
    image.save(buffer, format="JPEG", quality=88)
    return buffer.getvalue()


def upload(token, filename, payload, taken):
    """multipart/form-data by hand — stdlib only, same spirit as the agents."""
    boundary = uuid.uuid4().hex
    fields = {
        "deviceAssetId": f"lab-{filename}",
        "deviceId": "smallworlds-lab-seeder",
        "fileCreatedAt": taken.isoformat(),
        "fileModifiedAt": taken.isoformat(),
        "isFavorite": "false",
    }
    body = io.BytesIO()
    for key, value in fields.items():
        body.write(f"--{boundary}\r\n".encode())
        body.write(f'Content-Disposition: form-data; name="{key}"\r\n\r\n{value}\r\n'.encode())
    body.write(f"--{boundary}\r\n".encode())
    body.write(
        f'Content-Disposition: form-data; name="assetData"; filename="{filename}"\r\n'.encode()
    )
    content_type = mimetypes.guess_type(filename)[0] or "application/octet-stream"
    body.write(f"Content-Type: {content_type}\r\n\r\n".encode())
    body.write(payload)
    body.write(f"\r\n--{boundary}--\r\n".encode())

    request = urllib.request.Request(BASE + "/api/assets", data=body.getvalue(), method="POST")
    request.add_header("Content-Type", f"multipart/form-data; boundary={boundary}")
    request.add_header("Authorization", "Bearer " + token)
    with urllib.request.urlopen(request, timeout=120) as response:
        return json.loads(response.read() or b"null")


def main():
    session = api("/api/auth/login", {"email": EMAIL, "password": PASSWORD})
    token = session["accessToken"]
    print(f"Logged in as {session.get('userEmail')} (user {session.get('userId')})")

    random.seed(20260814)
    # Spread across two years and several days so the pod key layout is visible.
    dates = [
        datetime(2025, 10, 18, 10, 27, tzinfo=timezone.utc),
        datetime(2025, 10, 18, 14, 3, tzinfo=timezone.utc),
        datetime(2025, 12, 24, 19, 45, tzinfo=timezone.utc),
        datetime(2026, 3, 7, 8, 12, tzinfo=timezone.utc),
        datetime(2026, 8, 12, 16, 30, tzinfo=timezone.utc),
    ]
    created = 0
    for index in range(COUNT):
        taken = dates[index % len(dates)] + timedelta(minutes=index * 7)
        filename = f"IMG_{2000 + index}.jpg"
        payload = make_jpeg(index, taken)
        result = upload(token, filename, payload, taken)
        status = result.get("status")
        print(f"  {filename}  {taken:%Y-%m-%d}  {len(payload):>7} bytes  {status}")
        created += 1
    print(f"\nUploaded {created} assets.")


if __name__ == "__main__":
    sys.exit(main())
