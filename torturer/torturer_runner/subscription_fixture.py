"""Disposable HTTPS subscriptions for rendered native UI tests.

The fixture owns its loopback listener and temporary runner trust. Production
clients still fetch and validate HTTPS through their ordinary Go loader.
"""
from __future__ import annotations

import hashlib
import hmac
import json
import http.server
from pathlib import Path
import shutil
import secrets
import socket
import socketserver
import ssl
import tempfile
import threading
import time
import traceback
import uuid
from urllib.parse import urlsplit
import urllib.request

from .diagnostics import emit_streams
from .process_capture import exception_output, run_finite_capture


class UnixHTTPServer(http.server.ThreadingHTTPServer):
    """ADB can reach a filesystem socket across runner network namespaces."""

    def __init__(self, *args, **kwargs):
        self.address_family = socket.AF_UNIX
        super().__init__(*args, **kwargs)

    def server_bind(self):
        socketserver.TCPServer.server_bind(self)
        self.server_name = "localhost"
        self.server_port = 0

    def get_request(self):
        try:
            return super().get_request()
        except OSError as error:
            try:
                stdout, stderr = exception_output(error)
                if stderr and not stderr.endswith(b"\n"):
                    stderr += b"\n"
                stderr += "".join(traceback.format_exception(error)).encode(
                    "utf-8", errors="backslashreplace"
                )
                emit_streams("subscription-fixture-accept", stdout, stderr)
            except BaseException as diagnostic_error:
                error.add_note(
                    "subscription-fixture-accept diagnostic forwarding failed:\n"
                    + "".join(traceback.format_exception(diagnostic_error))
                )
            raise


def command(
    arguments: list[str],
    *,
    input_bytes: bytes | None = None,
    timeout_seconds: float = 30,
) -> bytes:
    try:
        result = run_finite_capture(
            arguments,
            timeout_seconds=timeout_seconds,
            input_bytes=input_bytes,
        )
    except BaseException as error:
        emit_streams("subscription-fixture", *exception_output(error))
        raise
    emit_streams("subscription-fixture", result.stdout, result.stderr)
    result.check_returncode()
    return result.stdout


class SubscriptionFixture:
    def __init__(
        self,
        profile: Path,
        directory: Path,
        platform: str,
        *,
        adb: list[str] | None = None,
        certificate_helper: Path | None = None,
        simulator_udid: str | None = None,
        simulator_temporary: bool = False,
    ):
        self.profile, self.directory, self.platform = profile, directory, platform
        self.adb = adb or ["adb"]
        self.certificate_helper = certificate_helper
        self.simulator_udid = simulator_udid or ""
        self.simulator_temporary = simulator_temporary
        if platform == "ios_simulator" and (not self.simulator_udid or not simulator_temporary):
            raise ValueError("iOS Simulator fixture trust requires its own disposable Simulator")
        self.server: http.server.ThreadingHTTPServer | None = None
        self.thread: threading.Thread | None = None
        self.trusted = False
        self.forwarded = False
        self.android_staged = False
        self.url = ""
        self.port = 0
        self.socket_path = ""
        self.certificate = directory / "ca.pem"
        self.key = directory / "key.pem"
        self.fingerprint = ""
        self.android_directory = "/data/local/tmp/dobbyvpn-subscription-" + uuid.uuid4().hex
        self.control_key = secrets.token_hex(32)
        self.control_path = "/__torturer__/" + uuid.uuid4().hex
        self.profile_bytes = b""
        self.control_condition = threading.Condition()
        self.subscription_gets = 0
        self.last_subscription_get_started_at_unix_ms = 0
        self.in_flight_gets = 0
        self.max_in_flight_gets = 0
        self.fail_next_gets = 0
        self.hold_gets = False
        self.release_permits = 0

    def start(self) -> str:
        self.directory.mkdir(parents=True, exist_ok=False)
        self.profile_bytes = self.profile.read_bytes()
        if self.platform == "windows":
            if self.certificate_helper is None:
                raise RuntimeError("Windows subscription fixture requires the prepared native test helper")
            command([str(self.certificate_helper), "--subscription-certificate", str(self.directory)])
        else:
            self._generate_certificate()
        self.fingerprint = hashlib.sha1(ssl.PEM_cert_to_DER_cert(self.certificate.read_text()), usedforsecurity=False).hexdigest()
        fixture = self

        class Handler(http.server.BaseHTTPRequestHandler):
            def address_string(self):
                return "localhost"

            def do_GET(self):
                path = urlsplit(self.path).path
                if path == "/failure":
                    self.send_error(503, "Synthetic subscription failure")
                    return
                if path == fixture.control_path:
                    fixture._serve_control(self)
                    return
                if path != "/subscription":
                    self.send_error(404)
                    return
                fixture._serve_subscription(self)

            def do_POST(self):
                path = urlsplit(self.path).path
                if path != fixture.control_path and not path.startswith(fixture.control_path + "/"):
                    self.send_error(404)
                    return
                fixture._serve_control(self)

        if self.platform == "android":
            # Keep this below AF_UNIX's path limit even in a long run directory.
            self.socket_path = str(Path(tempfile.gettempdir()) / ("dobbyvpn-subscription-" + uuid.uuid4().hex + ".sock"))
            self._save()
            self.server = UnixHTTPServer(self.socket_path, Handler)
            self.port = 49152 + secrets.randbelow(16384)
        else:
            self.server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
            self.port = self.server.server_port
        context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        context.load_cert_chain(self.certificate, self.key)
        self.server.socket = context.wrap_socket(self.server.socket, server_side=True)
        if self.platform == "macos":
            self.trusted = True
            self._save()
            command(["sudo", "-n", "security", "add-trusted-cert", "-d", "-r", "trustRoot", "-k", "/Library/Keychains/System.keychain", str(self.certificate)])
        elif self.platform == "windows":
            self.trusted = True
            self._save()
            command(["certutil", "-addstore", "-f", "Root", str(self.certificate)])
        elif self.platform == "android":
            certificate_hash = command([shutil.which("openssl") or "openssl", "x509", "-in", str(self.certificate), "-subject_hash_old", "-noout"]).decode().strip()
            self.android_staged = True
            self._save()
            command([*self.adb, "shell", "mkdir", self.android_directory])
            command([*self.adb, "shell", "cp", "-a", "/system/etc/security/cacerts/.", self.android_directory])
            command([*self.adb, "push", str(self.certificate), self.android_directory + "/" + certificate_hash + ".0"])
            command([*self.adb, "shell", "chmod", "644", self.android_directory + "/" + certificate_hash + ".0"])
            self.trusted = True
            self._save()
            command([*self.adb, "shell", "mount", "--bind", self.android_directory, "/system/etc/security/cacerts"])
            self.forwarded = True
            self._save()
            command([*self.adb, "reverse", "--no-rebind", f"tcp:{self.port}", f"localfilesystem:{self.socket_path}"])
        elif self.platform == "ios_simulator":
            # simctl has no per-certificate removal command. The iOS rendered
            # fixture therefore trusts only a run-owned Simulator which is
            # deleted after the test; reset here also handles normal teardown.
            self.trusted = True
            self._save()
            command(
                [
                    "xcrun",
                    "simctl",
                    "keychain",
                    self.simulator_udid,
                    "add-root-cert",
                    str(self.certificate),
                ],
                timeout_seconds=120,
            )
        elif self.platform != "untrusted":
            raise ValueError("Unsupported subscription fixture trust target")
        self.trusted = self.platform != "untrusted"
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()
        self.url = f"https://127.0.0.1:{self.port}/subscription"
        return self.url

    @property
    def control_url(self) -> str:
        if not self.url:
            raise RuntimeError("subscription fixture is not running")
        return f"https://127.0.0.1:{self.port}{self.control_path}"

    def _serve_subscription(self, request: http.server.BaseHTTPRequestHandler) -> None:
        with self.control_condition:
            self.subscription_gets += 1
            self.last_subscription_get_started_at_unix_ms = time.time_ns() // 1_000_000
            self.in_flight_gets += 1
            self.max_in_flight_gets = max(self.max_in_flight_gets, self.in_flight_gets)
            content = self.profile_bytes
            while self.hold_gets and self.release_permits == 0:
                self.control_condition.wait()
            if self.hold_gets:
                self.release_permits -= 1
            failed = self.fail_next_gets > 0
            if failed:
                self.fail_next_gets -= 1
        try:
            if failed:
                request.send_error(503, "Synthetic subscription failure")
                return
            request.send_response(200)
            request.send_header("Content-Type", "text/plain; charset=utf-8")
            request.send_header("Content-Length", str(len(content)))
            request.end_headers()
            request.wfile.write(content)
        finally:
            with self.control_condition:
                self.in_flight_gets -= 1
                self.control_condition.notify_all()

    def _serve_control(self, request: http.server.BaseHTTPRequestHandler) -> None:
        supplied = request.headers.get("X-DobbyVPN-Torturer-Key", "")
        if not hmac.compare_digest(supplied, self.control_key):
            request.send_error(403, "Fixture test control key rejected")
            return
        path = urlsplit(request.path).path
        if request.command == "GET" and path == self.control_path:
            state = self._control_stats_snapshot()
            body = json.dumps(state, sort_keys=True).encode("ascii")
            request.send_response(200)
            request.send_header("Content-Type", "application/json")
            request.send_header("Content-Length", str(len(body)))
            request.end_headers()
            request.wfile.write(body)
            return
        if request.command != "POST" or path not in {
            self.control_path + "/profile",
            self.control_path + "/hold",
            self.control_path + "/release-one",
            self.control_path + "/release",
            self.control_path + "/fail-next",
        }:
            request.send_error(404)
            return
        if path == self.control_path + "/profile":
            length = int(request.headers.get("Content-Length", "0"))
            if length <= 0 or length > 1 << 20:
                request.send_error(400, "Fixture profile size is invalid")
                return
            body = request.rfile.read(length)
            if len(body) != length:
                request.send_error(400, "Fixture profile body is incomplete")
                return
        else:
            body = b""
            if request.headers.get("Content-Length", "0") != "0":
                request.send_error(400, "Fixture control request must be empty")
                return
        with self.control_condition:
            if path == self.control_path + "/profile":
                self.profile_bytes = body
            elif path == self.control_path + "/hold":
                self.hold_gets = True
            elif path == self.control_path + "/release-one":
                if not self.hold_gets:
                    request.send_error(409, "Fixture responses are not held")
                    return
                self.release_permits += 1
                self.control_condition.notify_all()
            elif path == self.control_path + "/release":
                self.hold_gets = False
                self.release_permits = 0
                self.control_condition.notify_all()
            else:
                self.fail_next_gets += 1
        request.send_response(204)
        request.send_header("Content-Length", "0")
        request.end_headers()

    def _control_request(self, suffix: str = "", *, body: bytes | None = None) -> bytes:
        if not self.url:
            raise RuntimeError("subscription fixture is not running")
        request = urllib.request.Request(
            self.control_url + suffix,
            data=body,
            method="GET" if body is None else "POST",
            headers={"X-DobbyVPN-Torturer-Key": self.control_key},
        )
        context = ssl.create_default_context(cafile=str(self.certificate))
        with urllib.request.urlopen(request, context=context, timeout=5) as response:
            return response.read()

    def control_stats(self) -> dict[str, int]:
        if not self.url:
            raise RuntimeError("subscription fixture is not running")
        if self.platform == "android":
            if self.thread is None or not self.thread.is_alive():
                raise RuntimeError("subscription fixture is not running")
            return self._control_stats_snapshot()
        return json.loads(self._control_request())

    def _control_stats_snapshot(self) -> dict[str, int]:
        with self.control_condition:
            return {
                "subscription_gets": self.subscription_gets,
                "last_subscription_get_started_at_unix_ms": self.last_subscription_get_started_at_unix_ms,
                "in_flight_gets": self.in_flight_gets,
                "max_in_flight_gets": self.max_in_flight_gets,
                "server_now_unix_ms": time.time_ns() // 1_000_000,
            }

    def replace_response(self, content: bytes) -> None:
        if not content or len(content) > 1 << 20:
            raise ValueError("fixture response must contain 1 byte through 1 MiB")
        self._control_request("/profile", body=content)

    def hold_responses(self) -> None:
        self._control_request("/hold", body=b"")

    def release_responses(self) -> None:
        self._control_request("/release", body=b"")

    def release_one_response(self) -> None:
        self._control_request("/release-one", body=b"")

    def fail_next_response(self) -> None:
        self._control_request("/fail-next", body=b"")

    def _generate_certificate(self) -> None:
        openssl = shutil.which("openssl")
        if not openssl:
            raise RuntimeError("OpenSSL is required for the disposable subscription fixture")
        certificate_config = self.directory / "openssl.cnf"
        certificate_config.write_text(
            "[req]\ndistinguished_name=subject\nx509_extensions=extensions\nprompt=no\n"
            "[subject]\nCN=DobbyVPN Torturer " + uuid.uuid4().hex + "\n"
            "[extensions]\nsubjectAltName=IP:127.0.0.1\nbasicConstraints=critical,CA:TRUE\n",
            encoding="ascii",
        )
        command([openssl, "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-keyout", str(self.key),
                 "-out", str(self.certificate), "-days", "1", "-config", str(certificate_config)])

    def _save(self) -> None:
        (self.directory / "trust.json").write_text(json.dumps({
            "platform": self.platform, "adb": self.adb, "fingerprint": self.fingerprint,
            "trusted": self.trusted, "forwarded": self.forwarded, "port": self.port,
            "android_directory": self.android_directory, "android_staged": self.android_staged,
            "socket_path": self.socket_path,
            "control_key": self.control_key,
            "control_path": self.control_path,
            "simulator_udid": self.simulator_udid,
            "simulator_temporary": self.simulator_temporary,
        }), encoding="utf-8")

    @classmethod
    def cleanup_interrupted(cls, directory: Path) -> None:
        marker = directory / "trust.json"
        if not marker.is_file():
            return
        state = json.loads(marker.read_text(encoding="utf-8"))
        fixture = cls(
            directory / "unused", directory, state["platform"], adb=state["adb"],
            simulator_udid=state.get("simulator_udid"),
            simulator_temporary=state.get("simulator_temporary", False),
        )
        fixture.fingerprint = state["fingerprint"]
        fixture.trusted = state["trusted"]
        fixture.forwarded = state["forwarded"]
        fixture.port = state["port"]
        fixture.android_directory = state["android_directory"]
        fixture.android_staged = state["android_staged"]
        fixture.socket_path = state.get("socket_path", "")
        fixture.control_key = state.get("control_key", "")
        fixture.control_path = state.get("control_path", "")
        fixture.close()

    def _simulator_exists(self) -> bool:
        if not self.simulator_udid:
            return False
        inventory = json.loads(command(["xcrun", "simctl", "list", "devices", "-j"]))
        return any(
            isinstance(device, dict) and device.get("udid", "").upper() == self.simulator_udid.upper()
            for devices in inventory.get("devices", {}).values()
            if isinstance(devices, list)
            for device in devices
        )

    def close(self) -> None:
        errors: list[BaseException] = []
        with self.control_condition:
            self.hold_gets = False
            self.release_permits = 0
            self.control_condition.notify_all()
        if self.server is not None:
            if self.thread is not None:
                self.server.shutdown()
                self.thread.join()
            self.server.server_close()
        cleanup: list[list[str]] = []
        if self.trusted:
            if self.platform == "macos":
                # Root's -t removes both user and admin trust before the exact certificate.
                cleanup = [["sudo", "-n", "security", "delete-certificate", "-t", "-Z", self.fingerprint,
                            "/Library/Keychains/System.keychain"]]
            elif self.platform == "windows":
                cleanup = [["certutil", "-delstore", "Root", self.fingerprint]]
            elif self.platform == "ios_simulator" and self.simulator_temporary:
                try:
                    # If the disposable device was already deleted by outer
                    # interruption cleanup, its trust store is gone as well.
                    if self._simulator_exists():
                        cleanup = [["xcrun", "simctl", "keychain", self.simulator_udid, "reset"]]
                except BaseException as error:
                    errors.append(error)
        if self.android_staged:
            # A killed mount command may have succeeded before its caller returned.
            # Compare inode identity before unmounting: never remove another mount.
            try:
                staged = command([*self.adb, "shell", "stat", "-c", "%d:%i", self.android_directory]).strip()
                mounted = command([*self.adb, "shell", "stat", "-c", "%d:%i", "/system/etc/security/cacerts"]).strip()
                if staged == mounted:
                    command([*self.adb, "shell", "umount", "/system/etc/security/cacerts"])
                command([*self.adb, "shell", "rm", "-rf", self.android_directory])
            except BaseException as error:
                errors.append(error)
        if self.forwarded:
            try:
                mappings = command([*self.adb, "reverse", "--list"]).decode().splitlines()
                target = [f"tcp:{self.port}", f"localfilesystem:{self.socket_path}"]
                if any(line.split()[-2:] == target for line in mappings):
                    cleanup.append([*self.adb, "reverse", "--remove", f"tcp:{self.port}"])
            except BaseException as error:
                errors.append(error)
        for arguments in cleanup:
            try:
                command(arguments)
            except BaseException as error:
                errors.append(error)
        if self.socket_path:
            try:
                Path(self.socket_path).unlink(missing_ok=True)
            except BaseException as error:
                errors.append(error)
        if not errors and self.directory.exists():
            shutil.rmtree(self.directory)
        if errors:
            raise ExceptionGroup("Subscription fixture cleanup failed", errors)
