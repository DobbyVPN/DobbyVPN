"""The desktop mini adapter uses one backend-accepted configuration."""

import json
from pathlib import Path
from tempfile import TemporaryDirectory
import unittest

from torturer_runner.adapters.cli import CommandResult, CLIAdapter


class AcceptedSessionRunner:
    def __init__(self) -> None:
        self.commands: list[tuple[str, ...]] = []

    def run(self, command, *, timeout_seconds, input_bytes=None):
        self.commands.append(tuple(command[1:]))
        operation = command[1]
        if operation == "configure":
            result = {
                "session_id": "owner-1", "digest": "digest-1", "sequence": 2,
                "profiles": [{"index": 0, "protocol": "OUTLINE"}, {"index": 1, "protocol": "XRAY"}],
            }
        elif operation == "snapshot":
            result = {
                "session_id": "owner-1", "digest": "digest-1", "configured": True,
                "profiles": [{"index": 0}, {"index": 1}], "state": "CONFIGURED",
            }
        elif operation == "start":
            result = {"state": "CONNECTED", "generation": 3}
        elif operation == "stop":
            result = {"state": "CONFIGURED", "cleanup_complete": True}
        else:
            raise AssertionError(f"unexpected CLI command: {command}")
        return CommandResult(
            command=tuple(command), returncode=0,
            stdout=json.dumps({"ok": True, "result": result}).encode(),
        )


class CLIOrchestrationTests(unittest.TestCase):
    def test_selected_profile_uses_one_accepted_configuration(self) -> None:
        with TemporaryDirectory() as directory:
            root = Path(directory)
            cli = root / "dobby-cli"
            cli.touch()
            profile = root / "mixed.toml"
            profile.write_text(
                '[[Outline]]\nDescription = "synthetic Outline"\n'
                'Server = "vpn.invalid"\nPort = 443\nPassword = "synthetic"\n'
                '\n[[Xray]]\nDescription = "synthetic Xray"\n'
                'outbounds = [{ tag = "direct", protocol = "freedom" }]\n',
                encoding="utf-8",
            )
            runner = AcceptedSessionRunner()
            adapter = CLIAdapter(cli=cli, profile=profile, runner=runner)
            connections = adapter.discover_connections()
            self.assertEqual([(c.index, c.protocol) for c in connections], [(0, "OUTLINE"), (1, "XRAY")])
            adapter.select_connection(connections[1])
            self.assertTrue(adapter._configure(5))
            adapter._start_selected(5, "CONNECT_FAILED")
            adapter._stop(5, "DISCONNECT_FAILED")
            self.assertEqual([command[0] for command in runner.commands], ["configure", "snapshot", "start", "stop"])
            self.assertEqual(runner.commands[2], (
                "start", "--profile", "1", "--session-id", "owner-1",
                "--config-digest", "digest-1",
            ))
            self.assertEqual(runner.commands[3], (
                "stop", "--session-id", "owner-1", "--generation", "3",
            ))


if __name__ == "__main__":
    unittest.main()
