"""Tests for the small Render service command-line boundary."""

from __future__ import annotations

from argparse import Namespace
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

from torturer_provider import render_service


IMAGE_DIGEST = "sha256:" + "a" * 64


def start_args(output: Path) -> Namespace:
    return Namespace(
        run_id="12345",
        attempt="1",
        owner_id="tea-owner123",
        image_owner_id="tea-image123",
        image_path="ghcr.io/dobbyvpn/outline-wss@" + IMAGE_DIGEST,
        image_digest=IMAGE_DIGEST,
        region="oregon",
        output=output,
        listen_port=10000,
        timeout_seconds=30.0,
        poll_seconds=1.0,
    )


class FakeController:
    ready = Namespace(
        handle=Namespace(service_id="srv-ready123"),
        url="https://dobby-test.onrender.com",
    )
    instances: list["FakeController"] = []

    def __init__(self, api) -> None:
        self.api = api
        self.acquires: list[tuple[object, float, float]] = []
        self.releases: list[object] = []
        type(self).instances.append(self)

    def acquire(self, spec, *, timeout_seconds, poll_seconds):
        self.acquires.append((spec, timeout_seconds, poll_seconds))
        return self.ready

    def release(self, ready) -> None:
        self.releases.append(ready)


class FakeAPI:
    def __init__(self, token: str, **kwargs) -> None:
        self.token = token
        self.list_owner_ids: list[str] = []
        self.deleted_ids: list[str] = []
        self.existing_ids: list[str] = []

    def list_services(self, owner_id: str):
        self.list_owner_ids.append(owner_id)
        return self.records

    def delete_service(self, service_id: str) -> bool:
        self.deleted_ids.append(service_id)
        return True

    def exists(self, service_id: str) -> bool:
        self.existing_ids.append(service_id)
        return False


class RenderServiceCommandTests(unittest.TestCase):
    def tearDown(self) -> None:
        FakeController.instances.clear()

    def test_stop_by_run_id_is_scoped_to_owner_and_exact_run_name(self) -> None:
        api = FakeAPI("fixture-token")
        api.records = (
            Namespace(service_id="srv-other123", name="dobbyvpn-release-other"),
            Namespace(service_id="srv-target123", name="dobbyvpn-release-12345-1"),
        )
        args = Namespace(
            run_id="12345",
            attempt="1",
            owner_id="tea-owner123",
            timeout_seconds=20.0,
        )

        with (
            patch.dict(render_service.os.environ, {"RENDER_API_TOKEN": "fixture-token"}, clear=False),
            patch.object(render_service, "RenderAPI", return_value=api),
        ):
            self.assertEqual(render_service.stop(args), 0)

        self.assertEqual(api.list_owner_ids, ["tea-owner123"])
        self.assertEqual(api.deleted_ids, ["srv-target123"])
        self.assertEqual(api.existing_ids, ["srv-target123"])

    def test_stop_refuses_ambiguous_run_name_without_deleting(self) -> None:
        api = FakeAPI("fixture-token")
        api.records = (
            Namespace(service_id="srv-target123", name="dobbyvpn-release-12345-1"),
            Namespace(service_id="srv-target456", name="dobbyvpn-release-12345-1"),
        )
        args = Namespace(
            run_id="12345",
            attempt="1",
            owner_id="tea-owner123",
            timeout_seconds=20.0,
        )

        with (
            patch.dict(render_service.os.environ, {"RENDER_API_TOKEN": "fixture-token"}, clear=False),
            patch.object(render_service, "RenderAPI", return_value=api),
        ):
            with self.assertRaisesRegex(ValueError, "multiple Render services"):
                render_service.stop(args)

        self.assertEqual(api.deleted_ids, [])
        self.assertEqual(api.existing_ids, [])

    def test_start_releases_ready_service_when_profile_write_fails(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            output = Path(temporary) / "render"
            args = start_args(output)
            api = FakeAPI("fixture-token")

            with (
                patch.dict(render_service.os.environ, {"RENDER_API_TOKEN": "fixture-token"}, clear=False),
                patch.object(render_service, "RenderAPI", return_value=api),
                patch.object(render_service, "DisposableRenderController", FakeController),
                patch.object(Path, "write_text", side_effect=OSError("disk full")),
            ):
                with self.assertRaisesRegex(OSError, "disk full"):
                    render_service.start(args)

        self.assertEqual(len(FakeController.instances), 1)
        self.assertEqual(len(FakeController.instances[0].releases), 1)
        self.assertIs(FakeController.instances[0].releases[0], FakeController.ready)

    def test_start_writes_profile_and_attempt_scoped_service(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            output = Path(temporary) / "render"
            args = start_args(output)
            api = FakeAPI("fixture-token")
            with (
                patch.dict(render_service.os.environ, {"RENDER_API_TOKEN": "fixture-token"}, clear=False),
                patch.object(render_service, "RenderAPI", return_value=api),
                patch.object(render_service, "DisposableRenderController", FakeController),
                patch.object(render_service.time, "time", return_value=1_000),
            ):
                self.assertEqual(render_service.start(args), 0)
            self.assertIn("[[Outline]]", (output / "profile.toml").read_text())
            self.assertEqual((output / "deadline_epoch.txt").read_text(), "2800\n")
            self.assertEqual(FakeController.instances[0].acquires[0][0].name, "dobbyvpn-release-12345-1")


if __name__ == "__main__":
    unittest.main()
