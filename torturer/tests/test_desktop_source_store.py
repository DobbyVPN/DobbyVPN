from pathlib import Path
import json
import tempfile
import unittest
from unittest import mock

from torturer_runner import local_vm
from torturer_runner.desktop_source_store import isolate, restore


class DesktopSourceStoreTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.run = Path(temporary.name) / "run"
        self.home = Path(temporary.name) / "account"
        self.run.mkdir()
        self.source = self.home / ".dobbyvpn" / "configs" / "connection-url.txt"
        self.source.parent.mkdir(parents=True)

    def test_original_is_hidden_then_restored_exactly_after_run_writes(self):
        original = b"https://example.invalid/previous?value=synthetic\r\n"
        self.source.write_bytes(original)
        self.source.chmod(0o600)
        original_stat = self.source.stat()
        isolate(self.run, self.home)
        self.assertFalse(self.source.exists())
        self.source.write_bytes(b"https://example.invalid/current")
        restore(self.run, self.home)
        self.assertEqual(self.source.read_bytes(), original)
        self.assertEqual(self.source.stat().st_mode & 0o777, 0o600)
        if hasattr(original_stat, "st_uid"):
            restored_stat = self.source.stat()
            self.assertEqual(restored_stat.st_uid, original_stat.st_uid)
            self.assertEqual(restored_stat.st_gid, original_stat.st_gid)
        self.assertFalse((self.run / "source-url-backup").exists())
        restore(self.run, self.home)  # Repeated cleanup cannot erase the restored source.
        self.assertEqual(self.source.read_bytes(), original)

    def test_previously_absent_source_is_removed_after_run(self):
        isolate(self.run, self.home)
        self.source.write_text("https://example.invalid/current")
        restore(self.run, self.home)
        self.assertFalse(self.source.exists())

    def test_previously_absent_source_with_uncreated_home_restores_cleanly(self):
        fresh_home = self.run / "home-that-does-not-exist"
        isolate(self.run, fresh_home)
        restore(self.run, fresh_home)
        self.assertFalse(fresh_home.exists())

    def test_interruption_before_move_keeps_original(self):
        self.source.write_bytes(b"original")
        (self.run / "source-store.json").write_text(json.dumps({
            "schema": 1, "existed": True, "phase": "prepared",
            "original": self._metadata(self.source),
        }))
        restore(self.run, self.home)
        self.assertEqual(self.source.read_bytes(), b"original")

    def test_interruption_after_move_recovers_backup(self):
        self.source.write_bytes(b"original")
        isolate(self.run, self.home)
        record = self.run / "source-store.json"
        state = json.loads(record.read_text())
        state["phase"] = "prepared"
        record.write_text(json.dumps(state))
        restore(self.run, self.home)
        self.assertEqual(self.source.read_bytes(), b"original")

    def test_missing_backup_reports_loss_and_preserves_current_file(self):
        self.source.write_bytes(b"original")
        isolate(self.run, self.home)
        (self.run / "source-url-backup").unlink()
        self.source.write_bytes(b"current")
        with self.assertRaisesRegex(RuntimeError, "backup is missing"):
            restore(self.run, self.home)
        self.assertEqual(self.source.read_bytes(), b"current")

    def test_record_mismatch_does_not_consume_original_backup(self):
        self.source.write_bytes(b"original")
        isolate(self.run, self.home)
        record = self.run / "source-store.json"
        state = json.loads(record.read_text(encoding="utf-8"))
        state["original"]["sha256"] = "0" * 64
        record.write_text(json.dumps(state), encoding="utf-8")
        backup = self.run / "source-url-backup"
        with self.assertRaisesRegex(RuntimeError, "does not match its record"):
            restore(self.run, self.home)
        self.assertEqual(backup.read_bytes(), b"original")
        self.assertFalse(self.source.exists())

    def test_restore_retry_after_atomic_move_finishes_record_removal(self):
        self.source.write_bytes(b"original")
        self.source.chmod(0o640)
        isolate(self.run, self.home)
        with mock.patch(
            "torturer_runner.desktop_source_store._remove_record",
            side_effect=RuntimeError("interrupted after restore move"),
        ):
            with self.assertRaisesRegex(RuntimeError, "interrupted"):
                restore(self.run, self.home)
        self.assertFalse((self.run / "source-url-backup").exists())
        self.assertTrue((self.run / "source-store.json").exists())
        restore(self.run, self.home)
        self.assertEqual(self.source.read_bytes(), b"original")
        self.assertEqual(self.source.stat().st_mode & 0o777, 0o640)
        self.assertFalse((self.run / "source-store.json").exists())

    def test_backup_without_record_is_still_restored(self):
        self.source.write_bytes(b"exact private URL\r\n")
        self.source.chmod(0o604)
        isolate(self.run, self.home)
        (self.run / "source-store.json").unlink()
        self.source.write_bytes(b"new run URL")
        restore(self.run, self.home)
        self.assertEqual(self.source.read_bytes(), b"exact private URL\r\n")
        self.assertEqual(self.source.stat().st_mode & 0o777, 0o604)
        self.assertFalse((self.run / "source-url-backup").exists())

    def test_preexisting_record_does_not_authorize_cleanup_to_clear_source(self):
        self.source.write_bytes(b"account URL")
        (self.run / "source-store.json").write_text("{}", encoding="utf-8")
        local_vm._write_json(self.run / "platform.json", {
            "platform": "macos", "status": "cleanup-needed", "runtime": {},
            "desktop_source_store_isolated": False,
        })
        args = type("Args", (), {"run_dir": self.run, "platform": "macos", "timeout": 10})()
        with (
            mock.patch.object(local_vm, "_cleanup_logged"),
            mock.patch.object(local_vm, "_cleanup_launchd"),
            mock.patch.object(local_vm, "_desktop_source_store") as restore_store,
        ):
            self.assertEqual(local_vm.cleanup(args), 0)
        restore_store.assert_not_called()
        self.assertEqual(self.source.read_bytes(), b"account URL")

    def test_local_vm_cleanup_recovers_backup_when_record_is_missing(self):
        original = b"saved URL before interrupted cleanup"
        self.source.write_bytes(original)
        isolate(self.run, self.home)
        (self.run / "source-store.json").unlink()
        local_vm._write_json(self.run / "platform.json", {
            "platform": "macos", "status": "cleanup-needed", "runtime": {},
            "desktop_source_store_isolated": False,
        })
        args = type("Args", (), {"run_dir": self.run, "platform": "macos", "timeout": 10})()

        def recover(_run, _platform, _logs, _timeout, operation):
            self.assertEqual(operation, "restore")
            restore(self.run, self.home)

        with (
            mock.patch.object(local_vm, "_cleanup_logged"),
            mock.patch.object(local_vm, "_cleanup_launchd"),
            mock.patch.object(local_vm, "_desktop_source_store", side_effect=recover) as restore_store,
        ):
            self.assertEqual(local_vm.cleanup(args), 0)
        restore_store.assert_called_once()
        self.assertEqual(self.source.read_bytes(), original)
        self.assertFalse((self.run / "source-url-backup").exists())

    @staticmethod
    def _metadata(path):
        import hashlib
        import stat

        metadata = path.stat()
        return {
            "sha256": hashlib.sha256(path.read_bytes()).hexdigest(),
            "mode": stat.S_IMODE(metadata.st_mode),
            "uid": metadata.st_uid,
            "gid": metadata.st_gid,
        }

    def test_symlink_does_not_move_unrelated_source(self):
        target = self.run / "unrelated"
        target.write_bytes(b"unrelated")
        self.source.symlink_to(target)
        with self.assertRaisesRegex(RuntimeError, "symlink"):
            isolate(self.run, self.home)
        self.assertEqual(target.read_bytes(), b"unrelated")


if __name__ == "__main__":
    unittest.main()
