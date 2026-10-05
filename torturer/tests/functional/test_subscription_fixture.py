"""Exercise the disposable server without installing runner trust locally."""
import ssl
import importlib.util
import socket
import tempfile
import threading
import time
import unittest
import urllib.error
import urllib.request
from pathlib import Path
import json
from unittest.mock import patch

from torturer_runner.subscription_fixture import SubscriptionFixture


class SubscriptionFixtureTests(unittest.TestCase):
    def test_test_control_counts_loads_replaces_payload_and_controls_responses(self):
        with tempfile.TemporaryDirectory() as scratch:
            root = Path(scratch)
            profile = root / "profile.toml"
            initial = b"[[Outline]]\nDescription = \"before\"\n"
            replacement = b"[[Outline]]\nDescription = \"after\"\n"
            profile.write_bytes(initial)
            fixture = SubscriptionFixture(profile, root / "fixture", "untrusted")
            try:
                url = fixture.start()
                context = ssl.create_default_context(cafile=str(fixture.certificate))

                def control(method="GET", suffix="", body=None, key=None):
                    request = urllib.request.Request(
                        fixture.control_url + suffix,
                        data=body,
                        method=method,
                        headers={"X-DobbyVPN-Torturer-Key": key or fixture.control_key},
                    )
                    with urllib.request.urlopen(request, context=context, timeout=5) as response:
                        return response.status, response.read()

                with urllib.request.urlopen(url, context=context, timeout=5) as response:
                    self.assertEqual(initial, response.read())
                status, state = control()
                self.assertEqual(200, status)
                self.assertEqual({"subscription_gets": 1, "in_flight_gets": 0, "max_in_flight_gets": 1}, json.loads(state))

                with self.assertRaises(urllib.error.HTTPError) as denied:
                    control(key="incorrect-test-key")
                self.assertEqual(403, denied.exception.code)
                control("POST", "/profile", replacement)
                with urllib.request.urlopen(url + "?second=1", context=context, timeout=5) as response:
                    self.assertEqual(replacement, response.read())

                control("POST", "/fail-next")
                with self.assertRaises(urllib.error.HTTPError) as failure:
                    urllib.request.urlopen(url, context=context, timeout=5)
                self.assertEqual(503, failure.exception.code)
                with urllib.request.urlopen(url, context=context, timeout=5) as response:
                    self.assertEqual(replacement, response.read())

                control("POST", "/hold")
                outcome = []

                def held_get():
                    try:
                        with urllib.request.urlopen(url + "?slow=1", context=context, timeout=5) as response:
                            outcome.append((response.status, response.read()))
                    except BaseException as error:
                        outcome.append(error)

                thread = threading.Thread(target=held_get)
                thread.start()
                deadline = time.monotonic() + 3
                while time.monotonic() < deadline:
                    _, state = control()
                    if json.loads(state)["in_flight_gets"] == 1:
                        break
                    time.sleep(0.01)
                else:
                    self.fail("held subscription GET did not become observable")
                after_hold = b"[[Outline]]\nDescription = \"after hold\"\n"
                fixture.replace_response(after_hold)
                fixture.release_responses()
                thread.join(timeout=5)
                self.assertFalse(thread.is_alive(), "released subscription GET remained blocked")
                self.assertEqual([(200, replacement)], outcome)
                with urllib.request.urlopen(url + "?after-hold=1", context=context, timeout=5) as response:
                    self.assertEqual(after_hold, response.read())
                self.assertEqual(
                    {"subscription_gets": 6, "in_flight_gets": 0, "max_in_flight_gets": 1},
                    fixture.control_stats(),
                )
            finally:
                fixture.close()

    def test_tcp_fixture_and_cleanup_without_unix_socket_support(self):
        import torturer_runner.subscription_fixture as source
        spec = importlib.util.spec_from_file_location('torturer_runner.fixture_without_unix', source.__file__)
        module = importlib.util.module_from_spec(spec)
        unix_family = getattr(socket, 'AF_UNIX', None)
        if unix_family is not None:
            del socket.AF_UNIX
        try:
            spec.loader.exec_module(module)
            with tempfile.TemporaryDirectory() as scratch:
                root = Path(scratch)
                profile = root / 'profile'
                profile.write_bytes(b'synthetic profile\n')
                fixture = module.SubscriptionFixture(profile, root / 'fixture', 'untrusted')
                try:
                    url = fixture.start()
                    context = ssl.create_default_context(cafile=str(fixture.certificate))
                    with urllib.request.urlopen(url, context=context, timeout=5) as response:
                        self.assertEqual(profile.read_bytes(), response.read())
                finally:
                    fixture.close()
                module.SubscriptionFixture.cleanup_interrupted(fixture.directory)
                self.assertFalse(fixture.directory.exists())
        finally:
            if unix_family is not None:
                socket.AF_UNIX = unix_family

    @unittest.skipUnless(hasattr(socket, 'AF_UNIX'), 'Android host needs Unix sockets')
    def test_android_tls_over_filesystem_socket_and_cleanup(self):
        with tempfile.TemporaryDirectory() as scratch:
            root = Path(scratch)
            profile = root / 'profile'
            profile.write_bytes(b'synthetic profile\n')
            fixture = SubscriptionFixture(profile, root / 'fixture', 'android')
            from torturer_runner.subscription_fixture import command as real_command

            def command(arguments, **kwargs):
                if arguments[0] != 'adb':
                    return real_command(arguments, **kwargs)
                if arguments[1:] == ['reverse', '--list']:
                    return f'device tcp:{fixture.port} localfilesystem:{fixture.socket_path}\n'.encode()
                if 'stat' in arguments:
                    return b'7:101'
                return b''

            with patch('torturer_runner.subscription_fixture.command', side_effect=command) as calls:
                try:
                    fixture.start()
                    context = ssl.create_default_context(cafile=str(fixture.certificate))
                    with socket.socket(socket.AF_UNIX) as connection:
                        connection.connect(fixture.socket_path)
                        with context.wrap_socket(connection, server_hostname='127.0.0.1') as secured:
                            secured.sendall(b'GET /subscription HTTP/1.0\r\nHost: localhost\r\n\r\n')
                            with secured.makefile('rb') as response:
                                self.assertTrue(response.read().endswith(profile.read_bytes()))
                finally:
                    fixture.close()
                calls.assert_any_call(['adb', 'reverse', '--no-rebind', f'tcp:{fixture.port}', f'localfilesystem:{fixture.socket_path}'])
                calls.assert_any_call(['adb', 'reverse', '--remove', f'tcp:{fixture.port}'])
            self.assertFalse(Path(fixture.socket_path).exists())
            self.assertFalse(fixture.directory.exists())

    def test_tls_payload_failure_and_disposal(self):
        with tempfile.TemporaryDirectory() as scratch:
            root = Path(scratch)
            profile = root / "profile.toml"
            content = b'# synthetic subscription\nname = "\xce\xbb"\n'
            profile.write_bytes(content)
            fixture = SubscriptionFixture(profile, root / "fixture", "untrusted")
            try:
                url = fixture.start()
                with self.assertRaises(urllib.error.URLError):
                    urllib.request.urlopen(url, timeout=5)
                context = ssl.create_default_context(cafile=str(fixture.certificate))
                with urllib.request.urlopen(url + "?cold=1", context=context, timeout=5) as response:
                    self.assertEqual(content, response.read())
                with self.assertRaises(urllib.error.HTTPError) as failure:
                    urllib.request.urlopen(url.replace('/subscription', '/failure'), context=context, timeout=5)
                self.assertEqual(503, failure.exception.code)
            finally:
                fixture.close()
            self.assertFalse(fixture.directory.exists())
            self.assertEqual(content, profile.read_bytes())

    def test_interrupted_android_mount_only_unmounts_owned_identity(self):
        for mounted, expect_unmount in ((b'7:101', True), (b'7:202', False)):
            with self.subTest(mounted=mounted), tempfile.TemporaryDirectory() as scratch:
                root = Path(scratch)
                fixture = SubscriptionFixture(root / 'unused', root / 'fixture', 'android')
                fixture.directory.mkdir()
                fixture.android_staged = True
                fixture.trusted = True
                fixture._save()
                with patch('torturer_runner.subscription_fixture.command') as command:
                    command.side_effect = [b'7:101', mounted, b'', b'']
                    SubscriptionFixture.cleanup_interrupted(fixture.directory)
                    unmounted = any('umount' in call.args[0] for call in command.call_args_list)
                    self.assertEqual(expect_unmount, unmounted)
                self.assertFalse(fixture.directory.exists())

    def test_interrupted_android_reverse_preserves_another_listener(self):
        with tempfile.TemporaryDirectory() as scratch:
            root = Path(scratch)
            fixture = SubscriptionFixture(root / 'unused', root / 'fixture', 'android')
            fixture.directory.mkdir()
            fixture.forwarded = True
            fixture.port = 50001
            fixture.socket_path = str(root / 'owned.sock')
            fixture._save()
            with patch('torturer_runner.subscription_fixture.command', return_value=b'device tcp:50001 tcp:9000\n') as command:
                SubscriptionFixture.cleanup_interrupted(fixture.directory)
                command.assert_called_once_with(['adb', 'reverse', '--list'])
            self.assertFalse(fixture.directory.exists())

    def test_cleanup_group_preserves_each_original_failure(self):
        from torturer_runner.ui.journey import _exception_details
        with tempfile.TemporaryDirectory() as scratch:
            root = Path(scratch)
            fixture = SubscriptionFixture(root / 'unused', root / 'fixture', 'windows')
            fixture.directory.mkdir()
            fixture.trusted = True
            failure = OSError("synthetic trust cleanup failure")
            failure.add_note("original cleanup note")
            with patch('torturer_runner.subscription_fixture.command', side_effect=failure):
                with self.assertRaises(ExceptionGroup) as caught:
                    fixture.close()
            rendered = _exception_details(caught.exception)
            self.assertIn("OSError: synthetic trust cleanup failure", rendered)
            self.assertIn("original cleanup note", rendered)
            self.assertTrue(fixture.directory.exists())

    def test_interrupted_macos_cleanup_removes_exact_certificate_and_trust(self):
        with tempfile.TemporaryDirectory() as scratch:
            root = Path(scratch)
            fixture = SubscriptionFixture(root / 'unused', root / 'fixture', 'macos')
            fixture.directory.mkdir()
            fixture.trusted = True
            fixture.fingerprint = 'synthetic fingerprint'
            fixture._save()
            with patch('torturer_runner.subscription_fixture.command') as command:
                SubscriptionFixture.cleanup_interrupted(fixture.directory)
                command.assert_called_once_with([
                    'sudo', '-n', 'security', 'delete-certificate', '-t', '-Z',
                    'synthetic fingerprint', '/Library/Keychains/System.keychain'])
            self.assertFalse(fixture.directory.exists())
