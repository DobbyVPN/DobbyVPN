"""Small controller for one disposable Render web service.

The controller manages Render's control plane only.  It deliberately does
not create or serialize an Outline access key: that is image configuration
assembled by the provider job for the plaintext test profile. The Render
bearer token remains in that protected provider job.
"""

from __future__ import annotations

from dataclasses import dataclass
import json
import re
import time
import traceback
from typing import Any, Callable, Mapping, Protocol
from urllib.error import HTTPError
from urllib.parse import urlencode, urlparse
from urllib.request import Request, urlopen


_SERVICE_ID = re.compile(r"^srv-[A-Za-z0-9][A-Za-z0-9_-]*$")
_DEPLOY_ID = re.compile(r"^[A-Za-z0-9][A-Za-z0-9_-]*$")
_OWNER_ID = re.compile(r"^[A-Za-z0-9][A-Za-z0-9_-]*$")
_SERVICE_NAME = re.compile(r"^[a-z][a-z0-9-]{2,62}$")
_IMAGE_PATH = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._:/@-]{2,254}$")
_IMAGE_DIGEST = re.compile(r"^sha256:[0-9a-f]{64}$")
_READY_STATUSES = frozenset({"live"})
_FAILED_STATUSES = frozenset({"build_failed", "update_failed", "canceled", "deactivated"})
_REGIONS = frozenset({"frankfurt", "oregon", "ohio", "singapore", "virginia"})


class RenderAPIError(RuntimeError):
    """A provider failure retaining its original diagnostics in the chain."""

    def __init__(self, code: str, status: int | None = None) -> None:
        self.code = code
        self.status = status
        detail = f"{code}:{status}" if status is not None else code
        super().__init__(detail)


@dataclass(frozen=True)
class HTTPResponse:
    status: int
    payload: object


class RenderTransport(Protocol):
    def request(
        self,
        method: str,
        path: str,
        payload: Mapping[str, object] | None,
        headers: Mapping[str, str],
    ) -> HTTPResponse: ...


class _URLTransport:
    def __init__(self, base_url: str, timeout_seconds: float) -> None:
        self.base_url = base_url.rstrip("/")
        self.timeout_seconds = timeout_seconds

    def request(
        self,
        method: str,
        path: str,
        payload: Mapping[str, object] | None,
        headers: Mapping[str, str],
    ) -> HTTPResponse:
        body = None if payload is None else json.dumps(payload, sort_keys=True).encode("utf-8")
        request = Request(
            self.base_url + path,
            data=body,
            headers=dict(headers),
            method=method,
        )
        try:
            with urlopen(request, timeout=self.timeout_seconds) as response:
                raw = response.read()
                status = int(response.status)
        except HTTPError as error:
            try:
                error.add_note(f"response body: {error.read()!r}")
            except OSError as read_error:
                error.add_note("response body read failed:\n" + "".join(traceback.format_exception(read_error)))
            raise RenderAPIError("HTTP_ERROR", int(error.code)) from error
        if not raw:
            return HTTPResponse(status, {})
        try:
            payload_value = json.loads(raw.decode("utf-8"))
        except (UnicodeDecodeError, json.JSONDecodeError) as error:
            invalid = RenderAPIError("INVALID_JSON", status)
            invalid.add_note(f"response body: {raw!r}")
            raise invalid from error
        return HTTPResponse(status, payload_value)


def _require(value: object, pattern: re.Pattern[str], name: str) -> str:
    if not isinstance(value, str) or not pattern.fullmatch(value):
        raise ValueError(f"{name} has an invalid format")
    return value


@dataclass(frozen=True)
class RenderServiceSpec:
    """The only service configuration allowed by the disposable lane."""

    owner_id: str
    name: str
    image_owner_id: str
    image_path: str
    image_digest: str
    outline_config_yaml: str
    region: str = "oregon"
    plan: str = "free"

    def __post_init__(self) -> None:
        _require(self.owner_id, _OWNER_ID, "owner_id")
        _require(self.image_owner_id, _OWNER_ID, "image_owner_id")
        _require(self.name, _SERVICE_NAME, "name")
        _require(self.image_path, _IMAGE_PATH, "image_path")
        _require(self.image_digest, _IMAGE_DIGEST, "image_digest")
        if not self.image_path.endswith("@" + self.image_digest):
            raise ValueError("image_path must use the declared immutable digest")
        if self.region not in _REGIONS:
            raise ValueError("unsupported Render region")
        if self.plan != "free":
            raise ValueError("disposable controller only permits the free plan")
        if not isinstance(self.outline_config_yaml, str) or not self.outline_config_yaml or "\x00" in self.outline_config_yaml:
            raise ValueError("Outline config must be non-empty text")

    def payload(self) -> dict[str, object]:
        """Build the image-backed, one-instance, no-autodeploy request."""

        service_details: dict[str, object] = {
            "runtime": "image",
            "plan": self.plan,
            "region": self.region,
            "numInstances": 1,
        }
        payload: dict[str, object] = {
            "type": "web_service",
            "name": self.name,
            "ownerId": self.owner_id,
            "autoDeploy": "no",
            "image": {"ownerId": self.image_owner_id, "imagePath": self.image_path},
            "serviceDetails": service_details,
        }
        payload["secretFiles"] = [
            {"name": "config.yml", "content": self.outline_config_yaml}
        ]
        return payload

@dataclass(frozen=True)
class RenderServiceRecord:
    """Identity fields returned by the Render service list endpoint."""

    service_id: str
    name: str

    def __post_init__(self) -> None:
        _require(self.service_id, _SERVICE_ID, "service_id")
        _require(self.name, _SERVICE_NAME, "name")


@dataclass(frozen=True)
class RenderServiceHandle:
    service_id: str
    deploy_id: str | None

    def __post_init__(self) -> None:
        _require(self.service_id, _SERVICE_ID, "service_id")
        if self.deploy_id is not None:
            _require(self.deploy_id, _DEPLOY_ID, "deploy_id")
@dataclass(frozen=True)
class RenderServiceReady:
    handle: RenderServiceHandle
    url: str

    def __post_init__(self) -> None:
        parsed = urlparse(self.url)
        if parsed.scheme != "https" or not parsed.netloc or parsed.username or parsed.password:
            raise ValueError("Render service URL must be an HTTPS URL without credentials")
class RenderAPI:
    """Typed API operations with no response/token echoing."""

    def __init__(
        self,
        token: str,
        *,
        base_url: str = "https://api.render.com/v1",
        timeout_seconds: float = 20.0,
        transport: RenderTransport | None = None,
    ) -> None:
        if not isinstance(token, str) or not token or any(character.isspace() for character in token):
            raise ValueError("Render API token must be a non-empty single value")
        parsed = urlparse(base_url)
        if parsed.scheme != "https" or not parsed.netloc or parsed.username or parsed.password:
            raise ValueError("Render API base URL must be HTTPS without credentials")
        if timeout_seconds <= 0:
            raise ValueError("Render API timeout must be positive")
        self._token = token
        self._transport = transport or _URLTransport(base_url, timeout_seconds)

    def _request(
        self,
        method: str,
        path: str,
        payload: Mapping[str, object] | None = None,
        *,
        expected: frozenset[int],
        query: Mapping[str, object] | None = None,
    ) -> object:
        if not path.startswith("/") or "?" in path or "#" in path:
            raise ValueError("Render API path must be a fixed path")
        request_path = path
        if query:
            if any(not isinstance(key, str) or not key for key in query):
                raise ValueError("Render API query names must be non-empty strings")
            if any(not isinstance(value, (str, int)) or isinstance(value, bool) for value in query.values()):
                raise ValueError("Render API query values must be strings or integers")
            request_path = f"{path}?{urlencode(query)}"
        headers = {
            "Accept": "application/json",
            "Authorization": f"Bearer {self._token}",
            "Content-Type": "application/json",
        }
        response = self._transport.request(method, request_path, payload, headers)
        if response.status in expected:
            return response.payload
        error = RenderAPIError("UNEXPECTED_STATUS", response.status)
        error.add_note(f"response body: {response.payload!r}")
        raise error

    @staticmethod
    def _object(payload: object, code: str = "INVALID_RESPONSE") -> Mapping[str, Any]:
        if not isinstance(payload, dict):
            raise RenderAPIError(code)
        return payload

    def create_service(self, spec: RenderServiceSpec) -> RenderServiceHandle:
        payload = self._object(self._request("POST", "/services", spec.payload(), expected=frozenset({201})))
        service = self._object(payload.get("service"))
        service_id = service.get("id")
        if not isinstance(service_id, str) or not _SERVICE_ID.fullmatch(service_id):
            raise RenderAPIError("INVALID_SERVICE_ID")
        deploy_id = payload.get("deployId")
        if deploy_id is not None and (not isinstance(deploy_id, str) or not _DEPLOY_ID.fullmatch(deploy_id)):
            raise RenderAPIError("INVALID_DEPLOY_ID")
        return RenderServiceHandle(service_id, deploy_id)

    def service(self, service_id: str) -> Mapping[str, Any]:
        _require(service_id, _SERVICE_ID, "service_id")
        return self._object(self._request("GET", f"/services/{service_id}", expected=frozenset({200})))

    def list_services(
        self,
        owner_id: str,
        *,
        page_limit: int = 100,
    ) -> tuple[RenderServiceRecord, ...]:
        """List all web services in one owner workspace."""
        _require(owner_id, _OWNER_ID, "owner_id")
        if not 1 <= page_limit <= 100:
            raise ValueError("Render service-list bounds are invalid")
        records: list[RenderServiceRecord] = []
        cursor: str | None = None
        seen_cursors: set[str] = set()
        while True:
            query: dict[str, object] = {
                "ownerId": owner_id,
                "type": "web_service",
                "limit": page_limit,
            }
            if cursor is not None:
                query["cursor"] = cursor
            payload = self._request("GET", "/services", expected=frozenset({200}), query=query)
            if not isinstance(payload, list):
                raise RenderAPIError("INVALID_SERVICE_LIST")
            for item in payload:
                wrapper = self._object(item, "INVALID_SERVICE_LIST")
                service = self._object(wrapper.get("service"), "INVALID_SERVICE_LIST")
                try:
                    records.append(RenderServiceRecord(
                        service_id=service["id"],
                        name=service["name"],
                    ))
                except (KeyError, TypeError, ValueError) as error:
                    raise RenderAPIError("INVALID_SERVICE_LIST") from error
            if len(payload) < page_limit:
                return tuple(records)
            last = self._object(payload[-1], "INVALID_SERVICE_LIST")
            next_cursor = last.get("cursor")
            if not isinstance(next_cursor, str) or not next_cursor or next_cursor in seen_cursors:
                raise RenderAPIError("INVALID_SERVICE_CURSOR")
            seen_cursors.add(next_cursor)
            cursor = next_cursor

    def deploy(self, service_id: str, deploy_id: str) -> Mapping[str, Any]:
        _require(service_id, _SERVICE_ID, "service_id")
        _require(deploy_id, _DEPLOY_ID, "deploy_id")
        return self._object(self._request("GET", f"/services/{service_id}/deploys/{deploy_id}", expected=frozenset({200})))

    def delete_service(self, service_id: str) -> bool:
        _require(service_id, _SERVICE_ID, "service_id")
        try:
            self._request("DELETE", f"/services/{service_id}", expected=frozenset({204}))
            return True
        except RenderAPIError as error:
            if error.status == 404:
                return True
            raise

    def exists(self, service_id: str) -> bool:
        _require(service_id, _SERVICE_ID, "service_id")
        try:
            self.service(service_id)
            return True
        except RenderAPIError as error:
            if error.status == 404:
                return False
            raise


class DisposableRenderController:
    """Create one service, wait for readiness, and clean it up."""

    def __init__(
        self,
        api: RenderAPI,
        *,
        clock: Callable[[], float] = time.monotonic,
        sleeper: Callable[[float], None] = time.sleep,
    ) -> None:
        self.api = api
        self._clock = clock
        self._sleeper = sleeper

    def acquire(self, spec: RenderServiceSpec, *, timeout_seconds: float = 600.0, poll_seconds: float = 5.0) -> RenderServiceReady:
        if timeout_seconds <= 0 or poll_seconds <= 0:
            raise ValueError("Render readiness bounds must be positive")
        handle = self.api.create_service(spec)
        try:
            return self._wait_until_ready(handle, timeout_seconds, poll_seconds)
        except BaseException as original:
            try:
                self.api.delete_service(handle.service_id)
                if self.api.exists(handle.service_id):
                    raise RenderAPIError("DELETE_NOT_VERIFIED")
            except BaseException as cleanup_error:
                original.add_note(
                    "Render acquisition cleanup also failed:\n"
                    + "".join(traceback.format_exception(cleanup_error)).rstrip()
                )
            raise

    def _wait_until_ready(self, handle: RenderServiceHandle, timeout_seconds: float, poll_seconds: float) -> RenderServiceReady:
        deadline = self._clock() + timeout_seconds
        while True:
            service = self.api.service(handle.service_id)
            suspended = service.get("suspended")
            if suspended == "suspended":
                raise RenderAPIError("SERVICE_SUSPENDED")
            details = service.get("serviceDetails")
            if isinstance(details, dict):
                url = details.get("url")
                instances = details.get("numInstances")
                if instances is not None and instances != 1:
                    raise RenderAPIError("INSTANCE_COUNT_MISMATCH")
            else:
                url = None
            deploy_status: str | None = None
            if handle.deploy_id is not None:
                deploy = self.api.deploy(handle.service_id, handle.deploy_id)
                deploy_status_value = deploy.get("status")
                if isinstance(deploy_status_value, str):
                    deploy_status = deploy_status_value
                if deploy_status in _FAILED_STATUSES:
                    raise RenderAPIError("DEPLOY_FAILED")
            if deploy_status in _READY_STATUSES and isinstance(url, str):
                ready = RenderServiceReady(handle, url)
                return ready
            now = self._clock()
            if now >= deadline:
                raise RenderAPIError("READINESS_TIMEOUT")
            self._sleeper(min(poll_seconds, max(0.0, deadline - now)))

    def release(self, ready: RenderServiceReady) -> None:
        self.api.delete_service(ready.handle.service_id)
        if self.api.exists(ready.handle.service_id):
            raise RenderAPIError("DELETE_NOT_VERIFIED")


__all__ = [
    "DisposableRenderController",
    "HTTPResponse",
    "RenderAPI",
    "RenderAPIError",
    "RenderServiceHandle",
    "RenderServiceRecord",
    "RenderServiceReady",
    "RenderServiceSpec",
]
