from __future__ import annotations

import importlib.util
from pathlib import Path
import sys
import unittest

from torturer_provider.outline import OutlineWSSProfile


ROOT = Path(__file__).resolve().parents[2]
MODULE_PATH = ROOT / "torturer_provider" / "render.py"
SPEC = importlib.util.spec_from_file_location("render_provider_under_test", MODULE_PATH)
assert SPEC and SPEC.loader
RENDER = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = RENDER
SPEC.loader.exec_module(RENDER)


IMAGE_DIGEST = "sha256:" + "a" * 64


class FakeClock:
    def __init__(self) -> None:
        self.value = 0.0

    def __call__(self) -> float:
        return self.value

    def sleep(self, seconds: float) -> None:
        self.value += seconds


class FakeTransport:
    def __init__(self, *, failed: bool = False) -> None:
        self.failed = failed
        self.calls: list[tuple[str, str, object, dict[str, str]]] = []
        self.polls = 0
        self.deleted = False

    def request(self, method, path, payload, headers):
        self.calls.append((method, path, payload, dict(headers)))
        if method == "POST" and path == "/services":
            return RENDER.HTTPResponse(201, {"service": {"id": "srv-test123"}, "deployId": "dep-test123"})
        if method == "GET" and path == "/services/srv-test123":
            if self.deleted:
                raise RENDER.RenderAPIError("UNEXPECTED_STATUS", 404)
            return RENDER.HTTPResponse(200, {
                "suspended": "not_suspended",
                "serviceDetails": {
                    "numInstances": 1,
                    "url": "https://dobby-test.onrender.com" if self.polls else None,
                },
            })
        if method == "GET" and path == "/services/srv-test123/deploys/dep-test123":
            self.polls += 1
            return RENDER.HTTPResponse(200, {"status": "build_failed" if self.failed else ("live" if self.polls > 1 else "build_in_progress")})
        if method == "DELETE" and path == "/services/srv-test123":
            self.deleted = True
            return RENDER.HTTPResponse(204, {})
        raise AssertionError((method, path, payload))


class ResponseTransport:
    def __init__(self, responses: list[RENDER.HTTPResponse | RENDER.RenderAPIError]) -> None:
        self.responses = list(responses)
        self.calls: list[tuple[str, str]] = []

    def request(self, method, path, payload, headers):
        self.calls.append((method, path))
        response = self.responses.pop(0)
        if isinstance(response, BaseException):
            raise response
        return response


class InvalidCreateTransport:
    def __init__(self) -> None:
        self.calls: list[tuple[str, str]] = []

    def request(self, method, path, payload, headers):
        self.calls.append((method, path))
        if method == "POST" and path == "/services":
            return RENDER.HTTPResponse(201, {"service": {"id": "invalid"}})
        raise AssertionError((method, path, payload))


class PendingTransport(FakeTransport):
    def request(self, method, path, payload, headers):
        if method == "GET" and path == "/services/srv-test123":
            self.calls.append((method, path, payload, dict(headers)))
            if self.deleted:
                raise RENDER.RenderAPIError("UNEXPECTED_STATUS", 404)
            return RENDER.HTTPResponse(200, {
                "suspended": "not_suspended",
                "serviceDetails": {"numInstances": 1, "url": None},
            })
        if method == "GET" and path == "/services/srv-test123/deploys/dep-test123":
            self.calls.append((method, path, payload, dict(headers)))
            return RENDER.HTTPResponse(200, {"status": "build_in_progress"})
        return super().request(method, path, payload, headers)


class MalformedReadinessTransport(FakeTransport):
    def request(self, method, path, payload, headers):
        if method == "GET" and path == "/services/srv-test123":
            self.calls.append((method, path, payload, dict(headers)))
            if self.deleted:
                raise RENDER.RenderAPIError("UNEXPECTED_STATUS", 404)
            return RENDER.HTTPResponse(200, ["malformed"])
        return super().request(method, path, payload, headers)


class StickyDeleteTransport(MalformedReadinessTransport):
    def request(self, method, path, payload, headers):
        if method == "DELETE" and path == "/services/srv-test123":
            self.calls.append((method, path, payload, dict(headers)))
            return RENDER.HTTPResponse(204, {})
        return super().request(method, path, payload, headers)


def spec() -> object:
    return RENDER.RenderServiceSpec(
        owner_id="tea-test123",
        name="dobby-test-123",
        image_owner_id="tea-test123",
        image_path="ghcr.io/dobbyvpn/outline-wss@" + IMAGE_DIGEST,
        image_digest=IMAGE_DIGEST,
        outline_config_yaml=OutlineWSSProfile.random().config_yaml(10000),
    )


class RenderControllerTests(unittest.TestCase):
    def test_payload_is_one_free_image_service_with_outline_config(self) -> None:
        value = spec().payload()
        self.assertEqual(value["type"], "web_service")
        self.assertEqual(value["autoDeploy"], "no")
        self.assertEqual(value["serviceDetails"]["numInstances"], 1)
        self.assertEqual(value["serviceDetails"]["runtime"], "image")
        self.assertNotIn("healthCheckPath", value["serviceDetails"])
        self.assertEqual(value["secretFiles"][0]["name"], "config.yml")
        self.assertIn("secret:", value["secretFiles"][0]["content"])
        self.assertNotIn("token", repr(value).lower())

    def test_mutable_image_reference_is_rejected_even_with_a_digest_field(self) -> None:
        fields = spec().__dict__
        with self.assertRaisesRegex(ValueError, "immutable digest"):
            RENDER.RenderServiceSpec(**{**fields, "image_path": "ghcr.io/dobbyvpn/outline-wss:latest"})
        with self.assertRaisesRegex(ValueError, "immutable digest"):
            RENDER.RenderServiceSpec(
                **{**fields, "image_path": "ghcr.io/dobbyvpn/outline-wss@sha256:" + "b" * 64}
            )

    def test_outline_config_must_be_nonempty_text(self) -> None:
        fields = spec().__dict__
        with self.assertRaises(ValueError):
            RENDER.RenderServiceSpec(**{**fields, "outline_config_yaml": ""})

    def test_acquire_waits_for_live_https_service_and_release_verifies_deletion(self) -> None:
        transport = FakeTransport()
        clock = FakeClock()
        api = RENDER.RenderAPI("fixture-token", base_url="https://api.render.test/v1", transport=transport)
        controller = RENDER.DisposableRenderController(api, clock=clock, sleeper=clock.sleep)
        ready = controller.acquire(spec(), timeout_seconds=20, poll_seconds=1)
        self.assertEqual(ready.url, "https://dobby-test.onrender.com")
        controller.release(ready)
        self.assertTrue(transport.deleted)
        self.assertTrue(all("fixture-token" not in repr(call[:3]) for call in transport.calls))

    def test_transient_api_status_is_returned_without_a_retry(self) -> None:
        transport = ResponseTransport([
            RENDER.HTTPResponse(429, {"error": "rate limited"}),
        ])
        with self.assertRaisesRegex(RENDER.RenderAPIError, "UNEXPECTED_STATUS:429"):
            RENDER.RenderAPI("fixture-token", transport=transport).service("srv-test123")
        self.assertEqual(transport.calls, [("GET", "/services/srv-test123")])

    def test_malformed_service_list_is_rejected_instead_of_being_treated_as_empty(self) -> None:
        transport = ResponseTransport([RENDER.HTTPResponse(200, [{"service": {"id": "invalid"}}])])
        api = RENDER.RenderAPI("fixture-token", transport=transport)
        with self.assertRaisesRegex(RENDER.RenderAPIError, "INVALID_SERVICE_LIST"):
            api.list_services("tea-test123")

    def test_service_listing_follows_the_provider_cursor_without_a_page_cap(self) -> None:
        record = {
            "id": "srv-page123",
            "name": "dobby-page-123",
        }

        class PagedTransport:
            def __init__(self) -> None:
                self.calls: list[str] = []

            def request(self, method, path, payload, headers):
                self.calls.append(path)
                if "cursor=next" in path:
                    return RENDER.HTTPResponse(200, [])
                return RENDER.HTTPResponse(200, [{"service": record, "cursor": "next"}])

        transport = PagedTransport()
        services = RENDER.RenderAPI("fixture-token", transport=transport).list_services(
            "tea-test123", page_limit=1
        )
        self.assertEqual(tuple(service.service_id for service in services), ("srv-page123",))
        self.assertEqual(len(transport.calls), 2)

    def test_malformed_create_response_is_returned_without_name_recovery(self) -> None:
        transport = InvalidCreateTransport()
        api = RENDER.RenderAPI("fixture-token", transport=transport)
        controller = RENDER.DisposableRenderController(api)
        with self.assertRaisesRegex(RENDER.RenderAPIError, "INVALID_SERVICE_ID"):
            controller.acquire(spec(), timeout_seconds=5, poll_seconds=1)
        self.assertEqual(transport.calls, [("POST", "/services")])

    def test_cancellation_during_readiness_still_deletes_service(self) -> None:
        class Cancelled(BaseException):
            pass

        transport = FakeTransport()
        clock = FakeClock()

        def cancel(_seconds: float) -> None:
            raise Cancelled()

        api = RENDER.RenderAPI("fixture-token", transport=transport)
        controller = RENDER.DisposableRenderController(api, clock=clock, sleeper=cancel)
        with self.assertRaises(Cancelled):
            controller.acquire(spec(), timeout_seconds=20, poll_seconds=1)
        self.assertTrue(transport.deleted)

    def test_failed_deploy_is_deleted_before_failure_is_returned(self) -> None:
        transport = FakeTransport(failed=True)
        api = RENDER.RenderAPI("fixture-token", transport=transport)
        controller = RENDER.DisposableRenderController(api)
        with self.assertRaisesRegex(RENDER.RenderAPIError, "DEPLOY_FAILED"):
            controller.acquire(spec(), timeout_seconds=20)
        self.assertTrue(transport.deleted)

    def test_readiness_timeout_is_bounded_and_deletes_service(self) -> None:
        transport = PendingTransport()
        clock = FakeClock()
        api = RENDER.RenderAPI("fixture-token", transport=transport)
        controller = RENDER.DisposableRenderController(api, clock=clock, sleeper=clock.sleep)
        with self.assertRaisesRegex(RENDER.RenderAPIError, "READINESS_TIMEOUT"):
            controller.acquire(spec(), timeout_seconds=2, poll_seconds=1)
        self.assertTrue(transport.deleted)

    def test_malformed_readiness_response_is_not_treated_as_ready(self) -> None:
        transport = MalformedReadinessTransport()
        api = RENDER.RenderAPI("fixture-token", transport=transport)
        controller = RENDER.DisposableRenderController(api)
        with self.assertRaisesRegex(RENDER.RenderAPIError, "INVALID_RESPONSE"):
            controller.acquire(spec(), timeout_seconds=5, poll_seconds=1)
        self.assertTrue(transport.deleted)

    def test_readiness_failure_preserves_original_when_absence_check_fails(self) -> None:
        transport = StickyDeleteTransport()
        api = RENDER.RenderAPI("fixture-token", transport=transport)
        controller = RENDER.DisposableRenderController(api)
        with self.assertRaises(RENDER.RenderAPIError) as raised:
            controller.acquire(spec(), timeout_seconds=5, poll_seconds=1)
        self.assertEqual(str(raised.exception), "INVALID_RESPONSE")
        self.assertTrue(
            any("Render acquisition cleanup also failed" in note for note in raised.exception.__notes__)
        )
        self.assertTrue(any(method == "DELETE" and path == "/services/srv-test123" for method, path, *_ in transport.calls))

    def test_provider_errors_never_echo_api_token_or_response_body(self) -> None:
        class ErrorTransport:
            def request(self, method, path, payload, headers):
                raise RENDER.RenderAPIError("HTTP_ERROR", 401)

        api = RENDER.RenderAPI("fixture-token", transport=ErrorTransport())
        with self.assertRaises(RENDER.RenderAPIError) as raised:
            api.service("srv-test123")
        self.assertNotIn("fixture-token", str(raised.exception))
        self.assertNotIn("Authorization", str(raised.exception))


if __name__ == "__main__":
    unittest.main()
