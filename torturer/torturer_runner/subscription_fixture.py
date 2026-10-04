"""Disposable HTTPS subscriptions for rendered native UI tests.

The fixture owns its loopback listener and temporary runner trust. Production
clients still fetch and validate HTTPS through their ordinary Go loader.
"""
from __future__ import annotations

import hashlib
import json
import http.server
from pathlib import Path
import shutil
import ssl
import threading
import uuid
from urllib.parse import urlsplit

from .diagnostics import emit_streams
from .process_capture import exception_output, run_finite_capture


def command(arguments: list[str], *, input_bytes: bytes | None = None) -> bytes:
    try:
        result = run_finite_capture(arguments, timeout_seconds=30, input_bytes=input_bytes)
    except BaseException as error:
        emit_streams("subscription-fixture", *exception_output(error))
        raise
    emit_streams("subscription-fixture", result.stdout, result.stderr)
    result.check_returncode()
    return result.stdout


class SubscriptionFixture:
    def __init__(self, profile: Path, directory: Path, platform: str, *, adb: list[str] | None = None, certificate_helper: Path | None = None):
        self.profile, self.directory, self.platform = profile, directory, platform
        self.adb = adb or ["adb"]
        self.certificate_helper = certificate_helper
        self.server: http.server.ThreadingHTTPServer | None = None
        self.thread: threading.Thread | None = None
        self.trusted = False
        self.forwarded = False
        self.android_staged = False
        self.url = ""
        self.port = 0
        self.certificate = directory / "ca.pem"
        self.key = directory / "key.pem"
        self.fingerprint = ""
        self.android_directory = "/data/local/tmp/dobbyvpn-subscription-" + uuid.uuid4().hex

    def start(self) -> str:
        self.directory.mkdir(parents=True, exist_ok=False)
        if self.platform == "windows":
            if self.certificate_helper is None:
                raise RuntimeError("Windows subscription fixture requires the prepared native test helper")
            command([str(self.certificate_helper), "--subscription-certificate", str(self.directory)])
        else:
            self._generate_certificate()
        self.fingerprint = hashlib.sha1(ssl.PEM_cert_to_DER_cert(self.certificate.read_text()), usedforsecurity=False).hexdigest()
        fixture = self

        class Handler(http.server.BaseHTTPRequestHandler):
            def do_GET(self):
                path = urlsplit(self.path).path
                if path == "/failure":
                    self.send_error(503, "Synthetic subscription failure")
                    return
                if path != "/subscription":
                    self.send_error(404)
                    return
                content = fixture.profile.read_bytes()
                self.send_response(200)
                self.send_header("Content-Type", "text/plain; charset=utf-8")
                self.send_header("Content-Length", str(len(content)))
                self.end_headers()
                self.wfile.write(content)

        self.server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        context.load_cert_chain(self.certificate, self.key)
        self.server.socket = context.wrap_socket(self.server.socket, server_side=True)
        self.port = self.server.server_port
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
            command([*self.adb, "reverse", f"tcp:{self.port}", f"tcp:{self.port}"])
        elif self.platform != "untrusted":
            raise ValueError("Unsupported subscription fixture trust target")
        self.trusted = self.platform != "untrusted"
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()
        self.url = f"https://127.0.0.1:{self.port}/subscription"
        return self.url

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
        }), encoding="utf-8")

    @classmethod
    def cleanup_interrupted(cls, directory: Path) -> None:
        marker = directory / "trust.json"
        if not marker.is_file():
            return
        state = json.loads(marker.read_text(encoding="utf-8"))
        fixture = cls(directory / "unused", directory, state["platform"], adb=state["adb"])
        fixture.fingerprint = state["fingerprint"]
        fixture.trusted = state["trusted"]
        fixture.forwarded = state["forwarded"]
        fixture.port = state["port"]
        fixture.android_directory = state["android_directory"]
        fixture.android_staged = state["android_staged"]
        fixture.close()

    def close(self) -> None:
        errors: list[BaseException] = []
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
            cleanup.append([*self.adb, "reverse", "--remove", f"tcp:{self.port}"])
        for arguments in cleanup:
            try:
                command(arguments)
            except BaseException as error:
                errors.append(error)
        if not errors and self.directory.exists():
            shutil.rmtree(self.directory)
        if errors:
            raise ExceptionGroup("Subscription fixture cleanup failed", errors)
