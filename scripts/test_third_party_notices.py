"""OC-189 AC3: the licence policy refuses copyleft and unknown licences (python3 -m unittest scripts/test_third_party_notices.py)."""

import importlib.util
import json
import sys
import unittest
from pathlib import Path

HERE = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location("notices", HERE / "third-party-notices.py")
notices = importlib.util.module_from_spec(spec)
sys.modules["notices"] = notices  # dataclasses look their module up while it loads
spec.loader.exec_module(notices)
POLICY = json.loads((HERE / "licence-policy.json").read_text())


class PolicyTest(unittest.TestCase):
    def test_oc189_ac3_copyleft_and_unknown_refused(self):
        for lic in ["GPL-3.0-only", "GPL", "LGPL-2.1", "AGPL-3.0", "SSPL-1.0", "BUSL-1.1", "UNKNOWN"]:
            self.assertFalse(notices.allowed(lic, "some/dep", POLICY), lic)

    def test_oc189_ac3_permissive_allowed(self):
        for lic in ["MIT", "Apache-2.0", "BSD-3-Clause", "ISC", "0BSD", "MPL-2.0"]:
            self.assertTrue(notices.allowed(lic, "some/dep", POLICY), lic)

    def test_oc189_ac3_expressions(self):
        self.assertTrue(notices.allowed("(MIT OR GPL-3.0)", "x", POLICY), "OR: either side")
        self.assertFalse(notices.allowed("MIT AND GPL-3.0", "x", POLICY), "AND: both sides")
        self.assertTrue(notices.allowed("Apache-2.0 AND BSD-3-Clause", "x", POLICY))
        self.assertTrue(notices.allowed("Apache-2.0 WITH LLVM-exception", "x", POLICY), "WITH: the base licence")

    def test_oc189_ac3_font_licence_only_for_fonts(self):
        self.assertTrue(notices.allowed("OFL-1.1", "@fontsource/geist-sans", POLICY))
        self.assertFalse(notices.allowed("OFL-1.1", "left-pad", POLICY))

    def test_oc189_classify_licence_texts(self):
        self.assertEqual("MIT", notices.classify("Permission is hereby granted, free of charge, to any person"))
        self.assertEqual("BSD-3-Clause", notices.classify("Redistribution and use in source and binary forms ... Neither the name of"))
        self.assertEqual("BSD-2-Clause", notices.classify("Redistribution and use in source and binary forms, with or without modification"))
        self.assertEqual("GPL", notices.classify("GNU GENERAL PUBLIC LICENSE Version 3"))
        self.assertEqual("UNKNOWN", notices.classify("All rights reserved."))


if __name__ == "__main__":
    unittest.main()
