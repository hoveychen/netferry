import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
from build_routing_catalog import REGIONAL_RESTRICTIONS, gfw_domains


class GFWDomainTests(unittest.TestCase):
    def test_only_domain_anchored_rules_are_included(self):
        self.assertEqual(
            gfw_domains(["||example.com^", "||api.example.net", "|http://site.test/path", "||cdn*.site.test"]),
            {"example.com", "api.example.net"},
        )

    def test_exception_removes_overlapping_suffix(self):
        self.assertEqual(
            gfw_domains(["||example.com^", "||other.test^", "@@||www.example.com^", "@@||*.other.test^"]),
            set(),
        )

    def test_regional_restrictions_stay_exact_and_sourced(self):
        domains = [domain for domain, _, _ in REGIONAL_RESTRICTIONS]
        self.assertEqual(len(domains), len(set(domains)))
        self.assertTrue(all(domain.startswith("=") for domain in domains))
        self.assertTrue(all(evidence.startswith("https://") for _, _, evidence in REGIONAL_RESTRICTIONS))


if __name__ == "__main__":
    unittest.main()
