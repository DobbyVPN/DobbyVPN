from __future__ import annotations

import importlib.util
from pathlib import Path
import tempfile
import unittest


SCRIPT = Path(__file__).with_name("local_source_identity.py")
SPEC = importlib.util.spec_from_file_location("local_source_identity", SCRIPT)
if SPEC is None or SPEC.loader is None:
    raise RuntimeError(f"cannot load {SCRIPT}")
IDENTITY = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(IDENTITY)


class LocalSourceIdentityTests(unittest.TestCase):
    def setUp(self) -> None:
        self.temporary = tempfile.TemporaryDirectory()
        self.root = Path(self.temporary.name) / "source"
        self.root.mkdir()
        (self.root / "b.txt").write_bytes(b"b")
        (self.root / "a.txt").write_bytes(b"a")

    def tearDown(self) -> None:
        self.temporary.cleanup()

    def test_identity_is_deterministic_and_changes_with_source_bytes(self) -> None:
        first = IDENTITY.content_identities(self.root)
        second = IDENTITY.content_identities(self.root)
        self.assertEqual(first, second)
        (self.root / "a.txt").write_bytes(b"changed")
        self.assertNotEqual(first, IDENTITY.content_identities(self.root))
        for value in IDENTITY.content_identities(self.root):
            self.assertRegex(value, r"\A[0-9a-f]{40}\Z")

    def test_fixed_generated_roots_and_explicit_outputs_do_not_change_identity(self) -> None:
        baseline = IDENTITY.content_identities(self.root)
        (self.root / ".dobbyvpn-local-candidate").mkdir()
        (self.root / ".dobbyvpn-local-candidate/result.apk").write_bytes(b"candidate")
        (self.root / "runtime").mkdir()
        (self.root / "runtime/result.json").write_bytes(b"result")
        (self.root / "kmp_module/app/build").mkdir(parents=True)
        (self.root / "kmp_module/app/build/classes.bin").write_bytes(b"build")
        (self.root / "generated.apk").write_bytes(b"generated")
        self.assertEqual(
            baseline,
            IDENTITY.content_identities(self.root, frozenset({"generated.apk"})),
        )

    def test_source_symlink_target_is_part_of_the_identity(self) -> None:
        target = self.root / "a.txt"
        link = self.root / "link.txt"
        try:
            link.symlink_to(target)
        except (OSError, NotImplementedError) as error:
            self.skipTest(f"symlinks unavailable: {error}")
        first = IDENTITY.content_identities(self.root)
        link.unlink()
        link.symlink_to(self.root / "b.txt")
        self.assertNotEqual(first, IDENTITY.content_identities(self.root))

    def test_symlinked_source_root_uses_the_same_content(self) -> None:
        link = self.root.parent / "source-link"
        try:
            link.symlink_to(self.root, target_is_directory=True)
        except (OSError, NotImplementedError) as error:
            self.skipTest(f"symlinks unavailable: {error}")
        self.assertEqual(IDENTITY.content_identities(self.root), IDENTITY.content_identities(link))

    def test_irrelevant_explicit_exclusion_is_harmless(self) -> None:
        self.assertEqual(IDENTITY.main(["--root", str(self.root), "--exclude", "../escape"]), 0)


if __name__ == "__main__":
    unittest.main()
