"""Country display names for the node table."""

import os
import sys
import unittest

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

import vpn_utils  # noqa: E402


class TranslateCountryTest(unittest.TestCase):
    def test_known_country(self):
        self.assertEqual(vpn_utils.translate_country("Japan"), "日本")

    def test_previously_missing_countries(self):
        for name, zh in (("Estonia", "爱沙尼亚"), ("Peru", "秘鲁"), ("Venezuela", "委内瑞拉")):
            self.assertEqual(vpn_utils.translate_country(name), zh)

    def test_local_name_suffix_is_dropped(self):
        self.assertEqual(vpn_utils.translate_country("Croatia (LOCAL Name: Hrvatska)"), "克罗地亚")

    def test_unknown_country_is_kept(self):
        self.assertEqual(vpn_utils.translate_country("Atlantis"), "Atlantis")
        self.assertEqual(vpn_utils.translate_country(""), "")


if __name__ == "__main__":
    unittest.main()
