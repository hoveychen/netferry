import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
from build_service_catalog import expand


class CatalogExpansionTests(unittest.TestCase):
    def test_tagged_include_selects_only_matching_entries(self):
        files = {
            "ads": "include:service @ads\ntracker.test @ads\n",
            "service": "service.test\nad.service.test @ads\nfull:pixel.service.test @ads\n",
        }
        self.assertEqual(
            expand("ads", files, set(), {}, include_ads=True),
            {"ad.service.test", "=pixel.service.test", "tracker.test"},
        )

    def test_normal_service_omits_ad_entries(self):
        files = {"service": "service.test\nad.service.test @ads\nfull:api.service.test\n"}
        self.assertEqual(
            expand("service", files, set(), {}),
            {"service.test", "=api.service.test"},
        )


if __name__ == "__main__":
    unittest.main()
