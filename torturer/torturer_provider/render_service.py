"""Start or stop one disposable Render VPN for a hosted functional run.

The test service is deliberately boring: Render runs the pinned Outline image,
this command writes the generated profile, and the cleanup job deletes the
service named for that run. Public HTTPS services provide IP and transfer probes;
there is no second test server to provision or keep in sync.
"""

from __future__ import annotations

import argparse
from datetime import datetime, timezone
import json
import os
from pathlib import Path
import sys
import time


RENDER_LIFETIME_SECONDS = 2 * 60 * 60

from .outline import OutlineWSSProfile
from .render import DisposableRenderController, RenderAPI, RenderServiceSpec


def _emit_progress(event: str, **fields: object) -> None:
    print(
        json.dumps(
            {
                "kind": "dobbyvpn.render.progress",
                "event": event,
                "timestamp_utc": datetime.now(timezone.utc)
                .isoformat(timespec="milliseconds")
                .replace("+00:00", "Z"),
                **fields,
            },
            sort_keys=True,
            allow_nan=False,
        ),
        flush=True,
    )


def _timed_phase(name: str, operation, **fields):
    started = time.monotonic()
    progress = {"phase": name, **fields}
    _emit_progress("phase-start", **progress)
    try:
        result = operation()
    except BaseException as error:
        _emit_progress(
            "phase-finish",
            **progress,
            duration_seconds=time.monotonic() - started,
            error_type=type(error).__name__,
        )
        raise
    _emit_progress(
        "phase-finish",
        **progress,
        duration_seconds=time.monotonic() - started,
        completed=True,
    )
    return result


def start(args: argparse.Namespace) -> int:
    output = args.output
    output.mkdir(parents=True, exist_ok=True)
    profile = OutlineWSSProfile.random()
    name = f"dobbyvpn-release-{args.run_id}-{args.attempt}"
    spec = RenderServiceSpec(
        owner_id=args.owner_id,
        name=name,
        image_owner_id=args.image_owner_id,
        image_path=args.image_path,
        image_digest=args.image_digest,
        region=args.region,
        outline_config_yaml=profile.config_yaml(args.listen_port),
    )
    controller = DisposableRenderController(RenderAPI(os.environ["RENDER_API_TOKEN"]))
    service_started_at = time.time()
    ready = _timed_phase(
        "service-acquire",
        lambda: controller.acquire(
            spec,
            timeout_seconds=args.timeout_seconds,
            poll_seconds=args.poll_seconds,
        ),
    )
    try:
        _timed_phase(
            "profile-write",
            lambda: (
                (output / "profile.toml").write_text(
                    profile.client_toml(ready.url), encoding="utf-8"
                ),
                (output / "deadline_epoch.txt").write_text(
                    f"{int(service_started_at + RENDER_LIFETIME_SECONDS)}\n",
                    encoding="ascii",
                ),
            ),
            service_id=ready.handle.service_id,
        )
    except BaseException as error:
        try:
            _timed_phase(
                "service-release-after-profile-error",
                lambda: controller.release(ready),
                service_id=ready.handle.service_id,
            )
        except BaseException as cleanup_error:
            error.add_note(f"Render cleanup after profile write failure also failed: {cleanup_error}")
        raise
    print(f"render_service_id={ready.handle.service_id}")
    return 0


def stop(args: argparse.Namespace) -> int:
    api = RenderAPI(os.environ["RENDER_API_TOKEN"], timeout_seconds=args.timeout_seconds)
    if not args.run_id or not args.owner_id or not args.attempt:
        raise ValueError("stop requires run ID, attempt, and owner ID")
    name = f"dobbyvpn-release-{args.run_id}-{args.attempt}"
    matches = _timed_phase(
        "service-discovery",
        lambda: [record for record in api.list_services(args.owner_id) if record.name == name],
    )
    if len(matches) > 1:
        raise ValueError(f"multiple Render services match {name}")
    if not matches:
        print(f"render_service_absent={name}")
        return 0
    service_id = matches[0].service_id
    _timed_phase(
        "service-delete", lambda: api.delete_service(service_id), service_id=service_id
    )
    still_exists = _timed_phase(
        "service-delete-verification",
        lambda: api.exists(service_id),
        service_id=service_id,
    )
    if still_exists:
        raise RuntimeError(f"Render service {service_id} still exists after deletion")
    print(f"render_service_deleted={service_id}")
    return 0


def parser() -> argparse.ArgumentParser:
    root = argparse.ArgumentParser(description=__doc__)
    commands = root.add_subparsers(dest="command", required=True)
    start_parser = commands.add_parser("start")
    start_parser.add_argument("--run-id", required=True)
    start_parser.add_argument("--attempt", required=True)
    start_parser.add_argument("--owner-id", required=True)
    start_parser.add_argument("--image-owner-id", required=True)
    start_parser.add_argument("--image-path", required=True)
    start_parser.add_argument("--image-digest", required=True)
    start_parser.add_argument("--region", required=True)
    start_parser.add_argument("--output", type=Path, required=True)
    start_parser.add_argument("--listen-port", type=int, default=10000)
    start_parser.add_argument("--timeout-seconds", type=float, default=600.0)
    start_parser.add_argument("--poll-seconds", type=float, default=5.0)
    start_parser.set_defaults(handler=start)

    stop_parser = commands.add_parser("stop")
    stop_parser.add_argument("--run-id")
    stop_parser.add_argument("--attempt")
    stop_parser.add_argument("--owner-id")
    stop_parser.add_argument("--timeout-seconds", type=float, default=20.0)
    stop_parser.set_defaults(handler=stop)
    return root


def main(argv: list[str] | None = None) -> int:
    args = parser().parse_args(argv)
    try:
        return args.handler(args)
    except Exception as error:
        print(f"render service error: {error}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
