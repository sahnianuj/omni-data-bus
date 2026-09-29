#!/usr/bin/env python3
"""Regenerate docs/<release>/INDEX.md from the story files' header tables.

Each story file (OC-NNN-*.md) starts with:

    # OC-NNN — Title
    | Epic | Priority | Points | Depends on | Status |
    |---|---|---|---|---|
    | E1 Foundation | P0 | 5 | OC-001 | Not started |

Status must be one of: Not started, In progress, Implemented.
Implemented stories are struck through in the priority tables and the phase overview (not in
the per-phase story tables, which have a Status column).

Phases, the P1 wording and the release checklist are per release (RELEASES below). Story IDs are
numbered across releases, so each release's IDs must be contiguous but needn't start at OC-001.

Usage: python3 scripts/update-story-index.py [docs/release-1.0.0]
"""
import re
import sys
from collections import OrderedDict
from pathlib import Path

STATUSES = ("Not started", "In progress", "Implemented")


def priority_info(release: str) -> OrderedDict:
    minor = ".".join(release.split(".")[:2])
    return OrderedDict([
        ("P0", "Must have — release blocker"),
        ("P1", f"Should have — ships in {minor} unless explicitly descoped"),
        ("P2", "Nice to have"),
    ])


PRIORITY_INFO = priority_info("1.0.0")  # keys only, for validation

# Per-release configuration. Phases are in delivery order; each ends with something usable in the
# app, and every story must belong to exactly one phase (checked in main).
RELEASES = {
    "1.0.0": {
        "phases": [
            ("Skeleton", "Sign in, create a project, configure and test its PostgreSQL source and Couchbase target",
             ["OC-001", "OC-002", "OC-003", "OC-004", "OC-005", "OC-006", "OC-007", "OC-008", "OC-009", "OC-012"]),
            ("Phase 1 — Explore both sides", "Discover and browse the source schema with data preview; browse and create target buckets, scopes and collections",
             ["OC-010", "OC-011", "OC-013", "OC-014"]),
            ("Phase 2 — Design & map", "Pick tables, design target documents and keys, auto-map with a score, map the rest on the canvas, preview documents",
             ["OC-015", "OC-016", "OC-017", "OC-035", "OC-018", "OC-020", "OC-019", "OC-022"]),
            ("Phase 3 — Migrate", "Run a migration end to end: streaming reader, transform and writer, error handling, live progress bar",
             ["OC-023", "OC-024", "OC-025", "OC-026", "OC-027", "OC-028"]),
            ("Phase 4 — Harden & ship", "Resume after crashes, embedded tables, verification report, observability, packaging, security, E2E and benchmark",
             ["OC-029", "OC-021", "OC-030", "OC-031", "OC-032", "OC-033", "OC-034"]),
        ],
        "checklist": [
            "All P0 stories are Implemented (and P1 stories are Implemented or explicitly descoped with sign-off)",
            "CI green on `main`: lint, unit, integration, contract, E2E",
            "OC-034 full benchmark passes every gate on the reference environment; results committed under `bench/results/`",
            "Security review of OC-033 controls completed; no HIGH/CRITICAL vulnerabilities",
            "Image signed, SBOM attached, Helm chart published",
            "User guide and deployment docs updated",
        ],
    },
    "2.0.0": {
        "phases": [
            ("Phase 0 — 2.0 foundations", "Projects can target Couchbase or a directory; mappings move to spec v2 (relation tree) and 1.0 mappings upgrade transparently; every page shows the running release and build",
             ["OC-058", "OC-036", "OC-037"]),
            ("Phase 1 — Directory target", "Migrate into a folder of JSON files, one per document, named by primary key; look any document up by its key",
             ["OC-038", "OC-039", "OC-040", "OC-041"]),
            ("Phase 2 — Relationship mapping", "Build orders with an items[] array holding each product, through multi-level and many-to-many relations, without size surprises",
             ["OC-042", "OC-043", "OC-044", "OC-045", "OC-046"]),
            ("Phase 3 — Document composer", "Design any target JSON shape from scratch, bind objects and arrays to source relations, preview and validate it",
             ["OC-047", "OC-048", "OC-049"]),
            ("Phase 4 — Portable runner", "Export a finished mapping as a self-contained bash script or Kubernetes kit and run it in production without the tool",
             ["OC-050", "OC-051", "OC-052", "OC-053", "OC-054", "OC-055"]),
            ("Phase 5 — Harden & ship 2.0", "Upgrade path from 1.0, E2E suite and benchmarks for the new paths, release gate",
             ["OC-057", "OC-056"]),
        ],
        "checklist": [
            "All P0 stories are Implemented (and P1 stories are Implemented or explicitly descoped with sign-off)",
            "CI green on `main`: lint, unit, integration, contract, E2E (including the kind-cluster runner suite)",
            "OC-056 benchmarks pass every gate on the reference environment; results committed under `bench/results/`",
            "Security review of exported-credential handling (OC-052) and directory path controls (OC-038) completed; no HIGH/CRITICAL vulnerabilities",
            "1.0 → 2.0 upgrade rehearsed on a copy of a 1.0 metadata bucket (OC-057)",
            "Image and static `odb` binaries signed, SBOM attached, Helm chart published",
            "User guide, runner guide and deployment docs updated",
        ],
    },
}


def render_phases(stories: list, phases: list) -> list:
    by_id = {s["id"]: s for s in stories}
    out = ["## Delivery phases", "",
           "Stories are delivered in phases; each phase ends with something usable in the app. "
           "Within a phase, follow the listed order (each story's **Depends on** column is authoritative).", "",
           "| Phase | Stories (in order) | Points | Done | Status | Outcome |", "|---|---|---|---|---|---|"]
    current_found = False
    for name, outcome, ids in phases:
        group = [by_id[i] for i in ids]
        done = [s for s in group if s["status"] == "Implemented"]
        cells = ", ".join(f"~~{s['id']}~~" if s["status"] == "Implemented" else s["id"] for s in group)
        if len(done) == len(group):
            status = "✅ Done"
        elif not current_found:
            status = "🚧 Current"
            current_found = True
        else:
            status = "⬜ Planned"
        out.append(f"| {name} | {cells} | {sum(s['points'] for s in group)} | "
                   f"{len(done)}/{len(group)} ({sum(s['points'] for s in done)} pts) | {status} | {outcome} |")
    out.append("")

    out += ["## Stories by phase", "",
            "Each phase lists its stories in delivery order.", ""]
    current_found = False
    for name, outcome, ids in phases:
        group = [by_id[i] for i in ids]
        done = [s for s in group if s["status"] == "Implemented"]
        if len(done) == len(group):
            badge = "✅ Done"
        elif not current_found:
            badge = "🚧 Current"
            current_found = True
        else:
            badge = "⬜ Planned"
        out += [f"### {name} — {badge} ({len(done)}/{len(group)} stories, "
                f"{sum(s['points'] for s in done)}/{sum(s['points'] for s in group)} points)", "",
                f"_{outcome}._", "",
                "| # | ID | Story | Priority | Epic | Points | Depends on | Status |", "|---|---|---|---|---|---|---|---|"]
        for n, st in enumerate(group, 1):
            link = f"[{st['id']}]({st['file']})"
            cells = [link, st["title"], st["priority"], st["epic"], str(st["points"]), st["depends"]]
            if st["status"] == "Implemented":
                status = "✅ Implemented"
            elif st["status"] == "In progress":
                status = "🚧 In progress"
            else:
                status = "⬜ Not started"
            out.append(f"| {n} | " + " | ".join(cells + [status]) + " |")
        out.append("")
    return out


def parse_story(path: Path) -> dict:
    lines = path.read_text(encoding="utf-8").splitlines()
    m = re.match(r"#\s+(OC-\d{3})\s+—\s+(.+)", lines[0])
    if not m:
        raise ValueError(f"{path.name}: first line must be '# OC-NNN — Title'")
    for i, line in enumerate(lines):
        if line.strip().startswith("| Epic | Priority | Points |"):
            cells = [c.strip() for c in lines[i + 2].strip().strip("|").split("|")]
            epic, priority, points, depends, status = cells
            if status not in STATUSES:
                raise ValueError(f"{path.name}: status '{status}' not in {STATUSES}")
            if priority not in PRIORITY_INFO:
                raise ValueError(f"{path.name}: priority '{priority}' not in {list(PRIORITY_INFO)}")
            return {"id": m.group(1), "title": m.group(2), "file": path.name, "epic": epic,
                    "priority": priority, "points": int(points), "depends": depends, "status": status}
    raise ValueError(f"{path.name}: header table not found")


def row(n: int, s: dict) -> str:
    link = f"[{s['id']}]({s['file']})"
    cells = [link, s["title"], s["epic"], str(s["points"])]
    if s["status"] == "Implemented":
        cells = [f"~~{c}~~" for c in cells]
        status = "✅ Implemented"
    elif s["status"] == "In progress":
        status = "🚧 In progress"
    else:
        status = "⬜ Not started"
    return f"| {n} | " + " | ".join(cells + [status]) + " |"


def pct(a: int, b: int) -> str:
    return f"{(100 * a / b):.0f}%" if b else "0%"


def render(release: str, stories: list) -> str:
    cfg = RELEASES[release]
    prio = priority_info(release)
    total_pts = sum(s["points"] for s in stories)
    by_status = {st: [s for s in stories if s["status"] == st] for st in STATUSES}
    done_pts = sum(s["points"] for s in by_status["Implemented"])

    out = [f"# Release {release} — Story Index", "",
           "> Generated by `scripts/update-story-index.py` from each story's header table. "
           "Don't edit this file by hand: change a story's **Status** (`Not started` → `In progress` → `Implemented`) "
           "and re-run `python3 scripts/update-story-index.py docs/release-" + release + "`. "
           "In the priority tables and the phase overview, implemented stories are ~~struck through~~.", "",
           "Architecture, domain model, conventions, NFRs and the global Definition of Done: "
           "[00-architecture.md](00-architecture.md).", "",
           "## Snapshot", "",
           "| Metric | Stories | Story points |", "|---|---|---|",
           f"| **Total** | **{len(stories)}** | **{total_pts}** |",
           f"| ✅ Implemented | {len(by_status['Implemented'])} | {done_pts} |",
           f"| 🚧 In progress | {len(by_status['In progress'])} | {sum(s['points'] for s in by_status['In progress'])} |",
           f"| ⬜ Not started | {len(by_status['Not started'])} | {sum(s['points'] for s in by_status['Not started'])} |",
           f"| Not yet implemented (in progress + not started) | {len(stories) - len(by_status['Implemented'])} | {total_pts - done_pts} |",
           f"| **Completion** | {pct(len(by_status['Implemented']), len(stories))} | {pct(done_pts, total_pts)} |", ""]

    out += ["### By priority", "", "| Priority | Meaning | Stories | Points | Implemented (stories / points) |",
            "|---|---|---|---|---|"]
    for p, meaning in prio.items():
        group = [s for s in stories if s["priority"] == p]
        if not group:
            continue
        done = [s for s in group if s["status"] == "Implemented"]
        out.append(f"| {p} | {meaning} | {len(group)} | {sum(s['points'] for s in group)} | "
                   f"{len(done)} / {sum(s['points'] for s in done)} |")
    out.append("")

    epics = OrderedDict()
    for s in stories:
        epics.setdefault(s["epic"], []).append(s)
    out += ["### By epic", "", "| Epic | Stories | Points | Implemented (stories / points) |", "|---|---|---|---|"]
    for e, group in epics.items():
        done = [s for s in group if s["status"] == "Implemented"]
        out.append(f"| {e} | {len(group)} | {sum(s['points'] for s in group)} | "
                   f"{len(done)} / {sum(s['points'] for s in done)} |")
    out.append("")

    out += ["## Stories by priority", ""]
    for p, meaning in prio.items():
        group = [s for s in stories if s["priority"] == p]
        if not group:
            continue
        out += [f"### {p} — {meaning} ({len(group)} stories, {sum(s['points'] for s in group)} points)", "",
                "| # | ID | Story | Epic | Points | Status |", "|---|---|---|---|---|---|"]
        out += [row(n, s) for n, s in enumerate(group, 1)]
        out.append("")

    out += render_phases(stories, cfg["phases"])
    out += ["## Release checklist", ""] + [f"- [ ] {item}" for item in cfg["checklist"]] + [""]
    return "\n".join(out)


def main() -> None:
    release_dir = Path(sys.argv[1] if len(sys.argv) > 1 else "docs/release-1.0.0")
    stories = sorted((parse_story(p) for p in release_dir.glob("OC-*.md")), key=lambda s: s["id"])
    ids = [int(s["id"][3:]) for s in stories]
    if not ids or ids != list(range(ids[0], ids[0] + len(ids))):
        sys.exit(f"story numbering has gaps or duplicates: {ids}")
    release = release_dir.name.removeprefix("release-")
    if release not in RELEASES:
        sys.exit(f"no phase configuration for release {release}; add it to RELEASES")
    phased = [i for _, _, ids in RELEASES[release]["phases"] for i in ids]
    if sorted(phased) != [s["id"] for s in stories]:
        sys.exit(f"every story must be in exactly one phase; check RELEASES: {sorted(set(s['id'] for s in stories) ^ set(phased))}")
    release = release_dir.name.removeprefix("release-")
    (release_dir / "INDEX.md").write_text(render(release, stories), encoding="utf-8")
    print(f"INDEX.md updated: {len(stories)} stories, {sum(s['points'] for s in stories)} points")


if __name__ == "__main__":
    main()
