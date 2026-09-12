"""Construction of one disposable Outline WSS profile.

The Render controller owns the control-plane service lifecycle.  This
module owns the run-scoped Outline input that the provider workflow installs
into the pinned image and hands to the prepared client as a plaintext test
artifact. The provider's Render token and image secret file remain job-local.
"""

from __future__ import annotations

from dataclasses import dataclass
import re
import secrets
from typing import Mapping
from urllib.parse import urlparse


_CIPHER = "chacha20-ietf-poly1305"
_HOST = re.compile(r"^[A-Za-z0-9.-]{1,253}$")


def _port(value: object) -> int:
    if isinstance(value, bool) or not isinstance(value, int) or not 1 <= value <= 65535:
        raise ValueError("listen port must be an integer between 1 and 65535")
    return value


@dataclass(frozen=True)
class OutlineWSSProfile:
    """One disposable key and shared path prefix for stream and packet WSS."""

    web_path: str
    secret: str

    @classmethod
    def random(cls) -> "OutlineWSSProfile":
        """Create the run-scoped path and protocol key."""

        return cls(
            web_path=f"/dobby-{secrets.token_hex(16)}",
            secret=secrets.token_hex(32),
        )

    @property
    def stream_path(self) -> str:
        return f"{self.web_path}/tcp"

    @property
    def packet_path(self) -> str:
        return f"{self.web_path}/udp"

    def config_yaml(self, listen_port: int) -> str:
        """Return the Render image secret-file contents.

        The generated config contains no ordinary TCP/UDP listener and binds
        the provider's single HTTP/WebSocket port.
        """

        listen_port = _port(listen_port)
        return (
            "web:\n"
            "  servers:\n"
            "    - id: dobby-render\n"
            "      listen:\n"
            f'        - "0.0.0.0:{listen_port}"\n'
            "\n"
            "services:\n"
            "  - listeners:\n"
            "      - type: websocket-stream\n"
            "        web_server: dobby-render\n"
            f'        path: "{self.stream_path}"\n'
            "      - type: websocket-packet\n"
            "        web_server: dobby-render\n"
            f'        path: "{self.packet_path}"\n'
            "    keys:\n"
            "      - id: dobby-run\n"
            f"        cipher: {_CIPHER}\n"
            f'        secret: "{self.secret}"\n'
        )

    def client_block(self, service_url: str) -> Mapping[str, object]:
        """Build the in-memory DobbyVPN Outline block after service readiness."""

        parsed = urlparse(service_url)
        if parsed.scheme != "https" or not parsed.hostname or parsed.username or parsed.password:
            raise ValueError("service URL must be an HTTPS URL without credentials")
        host = parsed.hostname
        if not _HOST.fullmatch(host):
            raise ValueError("service URL host has an invalid format")
        if parsed.port not in (None, 443):
            raise ValueError("Render WSS service must use HTTPS port 443")
        return {
            "Method": _CIPHER,
            "Password": self.secret,
            "Server": host,
            "Port": 443,
            "WebSocket": True,
            "WebSocketPath": self.web_path,
            "DisguisePrefix": "POST ",
        }

    def client_toml(self, service_url: str) -> str:
        """Serialize the trusted client block in DobbyVPN's public TOML shape.

        This serializer is intentionally kept beside the provider contract so
        the hosted qualification does not invent a second profile format. Values are
        validated by :meth:`client_block` before interpolation.
        """

        block = self.client_block(service_url)
        return (
            "[[Outline]]\n"
            'Description = "DobbyVPN Torturer disposable Render service"\n'
            f"WebSocket = {str(block['WebSocket']).lower()}\n"
            f'Server = "{block["Server"]}"\n'
            f"Port = {block['Port']}\n"
            f'Password = "{block["Password"]}"\n'
            f'WebSocketPath = "{block["WebSocketPath"]}"\n'
            f'DisguisePrefix = "{block["DisguisePrefix"]}"\n'
        )
