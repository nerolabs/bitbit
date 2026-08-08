#!/usr/bin/env python3
"""Truth-in-labelling check (research remediation, process suggestion).

M0's whole credibility rests on honest held-vs-closed accounting. The failure mode
the red team keeps finding is *docs-ahead-of-code*: a spec that asserts "closed /
shipped / one-way / covered" for something the code doesn't deliver. This is the
lightweight standing guard against that, run in CI at every build:

  1. STRICT — every test named in docs/design/claims-ledger.md must EXIST. A claim in
     the ledger points at the test/code path that delivers it; if that test is gone
     (renamed, deleted), the claim is now unbacked and the build fails. (Whether the
     test PASSES is the job of the normal test suite; this checks the LINKAGE.)

  2. ADVISORY — scan the canon (TENETS.md, design/m0.md) for strong "delivered" claim
     phrases and report any whose subject is not represented in the ledger, as a nudge
     to either add a claim+test or soften the language. Never fails the build.

Dependency-free (stdlib only). Run: python3 scripts/check_claims.py [--strict]
"""
import os
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
LEDGER = ROOT / "docs" / "design" / "claims-ledger.md"
CANON = [ROOT / "docs" / "TENETS.md", ROOT / "docs" / "design" / "m0.md"]

# Advisory: strong "this is delivered" phrasings whose subject should be a ledger claim.
CLAIM_PHRASES = [
    "one-way ratchet", "no discount", "no quiet capture", "never re-arm",
    "firewall holds", "bit-perfect or", "cannot be forged", "off the public mux",
]


def referenced_tests(text: str) -> set:
    return set(re.findall(r"\bTest[A-Za-z0-9_]+\b", text))


def all_test_funcs() -> set:
    """Every `func TestXxx(` defined anywhere in the tree."""
    funcs = set()
    for dirpath, dirs, files in os.walk(ROOT):
        dirs[:] = [d for d in dirs if d not in (".git", "node_modules", "dist")]
        for f in files:
            if f.endswith("_test.go"):
                try:
                    src = Path(dirpath, f).read_text(errors="ignore")
                except OSError:
                    continue
                funcs.update(re.findall(r"func\s+(Test[A-Za-z0-9_]+)\s*\(", src))
    return funcs


def main() -> int:
    strict = "--strict" in sys.argv
    if not LEDGER.exists():
        print(f"error: {LEDGER.relative_to(ROOT)} not found", file=sys.stderr)
        return 1
    ledger_text = LEDGER.read_text()
    referenced = referenced_tests(ledger_text)
    if not referenced:
        print("error: the claims ledger references no tests — every claim must point at one", file=sys.stderr)
        return 1

    defined = all_test_funcs()
    missing = sorted(referenced - defined)
    if missing:
        print("FAIL — the claims ledger points at tests that do not exist (a claim is now unbacked):", file=sys.stderr)
        for t in missing:
            print(f"  - {t}", file=sys.stderr)
        print("Fix: restore/rename the test, or update docs/design/claims-ledger.md.", file=sys.stderr)
        return 1

    print(f"OK — all {len(referenced)} claim-backing tests in the ledger exist.")

    # Advisory pass (never fails, unless --strict): strong canon claims not in the ledger.
    ledger_lc = ledger_text.lower()
    advisories = []
    for path in CANON:
        if not path.exists():
            continue
        for i, line in enumerate(path.read_text().splitlines(), 1):
            low = line.lower()
            for phrase in CLAIM_PHRASES:
                if phrase in low and phrase not in ledger_lc:
                    advisories.append(f"  {path.relative_to(ROOT)}:{i}: '{phrase}' — assert it in the ledger with a test, or soften it")
    if advisories:
        print("\nADVISORY — strong 'delivered' claims in the canon not represented in the ledger:")
        print("\n".join(advisories))
        if strict:
            return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
