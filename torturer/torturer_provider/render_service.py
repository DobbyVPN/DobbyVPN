"""Start or stop the one disposable Render VPN used by a release run.

The test service is deliberately boring: Render runs the pinned Outline image,
this command writes the generated profile, and the final Release job deletes
the service named for that run. Public HTTPS services provide IP and transfer probes;
there is no second test server to provision or keep in sync.
"""

from __future__ import annotations

import argparse
import os
from pathlib import Path
import sys
import time


RENDER_LIFETIME_SECONDS = 30 * 60

from .outline import OutlineWSSProfile
from .render import DisposableRenderController, RenderAPI, RenderServiceSpec


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
    ready = controller.acquire(spec, timeout_seconds=args.timeout_seconds, poll_seconds=args.poll_seconds)
    try:
        (output / "profile.toml").write_text(profile.client_toml(ready.url), encoding="utf-8")
        (output / "deadline_epoch.txt").write_text(
            f"{int(service_started_at + RENDER_LIFETIME_SECONDS)}\n",
            encoding="ascii",
        )
    except BaseException as error:
        try:
            controller.release(ready)
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
    matches = [record for record in api.list_services(args.owner_id) if record.name == name]
    if len(matches) > 1:
        raise ValueError(f"multiple Render services match {name}")
    if not matches:
        print(f"render_service_absent={name}")
        return 0
    service_id = matches[0].service_id
    api.delete_service(service_id)
    if api.exists(service_id):
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
