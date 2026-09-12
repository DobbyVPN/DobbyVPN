from __future__ import annotations

from pathlib import Path
import unittest

from torturer_contract.functional.assertions import evaluate_assertion
from torturer_contract.functional.scenarios import TEST_SET


class ContractOwnershipTests(unittest.TestCase):
    def test_scenario_and_assertion_implementations_have_one_owner(self):
        package_root = Path(__file__).parents[2] / "torturer_contract/functional"
        python_sources = tuple(package_root.glob("*.py"))
        test_set_owners = sum(
            source.read_text(encoding="utf-8").count("\nTEST_SET:")
            for source in python_sources
        )
        assertion_function_owners = sum(
            source.read_text(encoding="utf-8").count("def evaluate_assertion(")
            for source in python_sources
        )
        self.assertEqual(test_set_owners, 1)
        self.assertEqual(assertion_function_owners, 1)
        self.assertEqual(evaluate_assertion.__module__, "torturer_contract.functional.assertions")
        self.assertTrue(TEST_SET)


if __name__ == "__main__":
    unittest.main()
