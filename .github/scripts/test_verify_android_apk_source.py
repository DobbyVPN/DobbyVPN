import importlib.util
from pathlib import Path
import unittest


SCRIPT = Path(__file__).with_name("verify_android_apk_source.py")
SPEC = importlib.util.spec_from_file_location("verify_android_apk_source", SCRIPT)
assert SPEC and SPEC.loader
VERIFY = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(VERIFY)


class VerifyAndroidApkSourceTests(unittest.TestCase):
    def setUp(self):
        self.sha = "a" * 40
        self.repo = "DobbyVPN/DobbyVPN"

    def code(self, sha=None, link=None):
        sha = self.sha if sha is None else sha
        link = f"https://github.com/{self.repo}/tree/{self.sha}" if link is None else link
        return (
            '.class public final Lcom/dobby/vpn/BuildConfig;\n'
            f'.field public static final PROJECT_REPOSITORY_COMMIT:Ljava/lang/String; = "{sha}"\n'
            f'.field public static final PROJECT_REPOSITORY_COMMIT_LINK:Ljava/lang/String; = "{link}"\n'
        )

    def testCommitAndLinkPass(self):
        VERIFY.verify_code(self.code(), self.sha, self.repo)

    def testRejectsMissingOrWrongCommit(self):
        for wrong in ("N/A", "b" * 40):
            with self.subTest(wrong=wrong), self.assertRaises(VERIFY.VerificationError):
                VERIFY.verify_code(self.code(sha=wrong), self.sha, self.repo)

    def testRejectsWrongLinkOrDuplicateField(self):
        with self.assertRaises(VERIFY.VerificationError):
            VERIFY.verify_code(self.code(link="https://example.invalid/source"), self.sha, self.repo)
        with self.assertRaises(VERIFY.VerificationError):
            VERIFY.verify_code(self.code() + self.code(), self.sha, self.repo)


if __name__ == "__main__":
    unittest.main()
