"""Exercise the disposable server without installing runner trust locally."""
import ssl
import tempfile
import unittest
import urllib.error
import urllib.request
from pathlib import Path
from unittest.mock import patch

from torturer_runner.subscription_fixture import SubscriptionFixture


class SubscriptionFixtureTests(unittest.TestCase):
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
                with urllib.request.urlopen(url, context=context, timeout=5) as response:
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
