from pathlib import Path
import tempfile
import unittest

from fdroid_preflight import APP_ID, stage_app_metadata


class FdroidPreflightMetadataTests(unittest.TestCase):
    def test_stages_owned_baseline_when_upstream_catalog_app_metadata_is_missing(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            source_root = root / "source"
            owned = source_root / ".github" / "fdroid" / f"{APP_ID}.yml"
            owned.parent.mkdir(parents=True)
            contents = b"RepoType: git\nCurrentVersion: 1.5.0\n"
            owned.write_bytes(contents)

            # Keep the upstream repository present for its shared resources,
            # but omit its app recipe as it may be delisted independently.
            fdroiddata = root / "fdroiddata"
            (fdroiddata / "config").mkdir(parents=True)
            (fdroiddata / "srclibs").mkdir()
            self.assertFalse((fdroiddata / "metadata" / f"{APP_ID}.yml").exists())

            staged = root / "workspace" / "metadata" / f"{APP_ID}.yml"
            stage_app_metadata(source_root, staged)

            self.assertEqual(staged.read_bytes(), contents)


if __name__ == "__main__":
    unittest.main()
