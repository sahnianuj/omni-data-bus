#!/usr/bin/env python3
"""Regenerate docs/<release>/INDEX.md from the story files' header tables.

Each story file (OC-NNN-*.md) starts with:

    # OC-NNN — Title
    | Epic | Priority | Points | Depends on | Status |
    |---|---|---|---|---|
    | E1 Foundation | P0 | 5 | OC-001 | Not started |

Status must be one of: Not started, In progress, Implemented, Descoped.
Implemented stories are struck through in the priority tables and the phase overview (not in
the per-phase story tables, which have a Status column). Descoped stories keep their file and ID
(numbering stays gap-free) but don't count anywhere: they're listed in their phase's table and in
a "Descoped" section only.

The theme (what the release ships, shown first), phases, the P1 wording and the release checklist are per release (RELEASES below). Story IDs are
numbered across releases: unique and gap-free over all docs/release-* folders. A story added to an
earlier release takes the next free ID, so a release's IDs needn't be contiguous.

Usage: python3 scripts/update-story-index.py [docs/release-1.0.0]
"""
import re
import sys
from collections import OrderedDict
from pathlib import Path

STATUSES = ("Not started", "In progress", "Implemented", "Descoped")


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
        "theme": ("PostgreSQL → Couchbase, end to end in the browser: connect, design the documents, migrate, verify.", [
            ("Projects & connections", "Sign in to a workspace, create a project, and test and save its PostgreSQL source and Couchbase (self-managed or Capella) target once, with credentials encrypted"),
            ("Explore both sides", "Discover source schemas with data preview; browse and create buckets, scopes and collections"),
            ("Design & map", "Design target documents and keys, auto-map with a score, finish on a drag-and-drop canvas with transformations, embed related tables, preview real documents"),
            ("Migrate", "Snapshot-consistent parallel streaming with live progress, pause/resume, crash recovery, dead-letter log and a verification report"),
            ("Production-ready", "Metrics and tracing, signed image and Helm chart, security hardening, E2E suite and benchmark gate"),
        ]),
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
        "theme": ("Any document shape, a second target kind, and migrations that run without the tool.", [
            ("Directory target", "Migrate into a folder of JSON files, one per document, named by primary key and looked up by key"),
            ("Relationship mapping", "One document from many tables: multi-level and many-to-many arrays, filters and aggregates, sorted-merge for large arrays and a document-size guard"),
            ("Document composer", "Design any JSON shape from scratch or import the customer's own design, get suggested bindings, group rows into one document, and fix differences from the design with guidance"),
            ("Portable runner", "Export a mapping as a self-contained `migrate.sh` / bundle and run it headless with `odb run`, credentials sealed, plaintext or external (Kubernetes kit descoped)"),
            ("All-in-one image", "One Docker image with the app and a bundled single-node Couchbase Server CE, plus a customer run kit"),
            ("Computed fields", "Add any field whose value is fixed, a template or a JavaScript expression over the document's other fields (`concat()`, `substring()`, …), with autocomplete and live results on sample documents"),
            ("Chained embeds", "Once an object is embedded, its own foreign keys and child tables are offered under it on the source side, so order → items → product → brand → supplier builds one click at a time"),
            ("Upgrade", "1.0 projects and mappings upgrade transparently to mapping spec v2; every page shows the release and build"),
        ]),
        "phases": [
            ("Phase 0 — 2.0 foundations", "Projects can target Couchbase or a directory; mappings move to spec v2 (relation tree) and 1.0 mappings upgrade transparently; every page shows the running release and build",
             ["OC-058", "OC-036", "OC-037"]),
            ("Phase 1 — Directory target", "Migrate into a folder of JSON files, one per document, named by primary key; look any document up by its key",
             ["OC-038", "OC-039", "OC-040", "OC-041", "OC-068", "OC-079"]),
            ("Phase 2 — Relationship mapping", "Build orders with an items[] array holding each product, through multi-level and many-to-many relations, without size surprises",
             ["OC-042", "OC-043", "OC-044", "OC-045", "OC-046", "OC-082"]),
            ("Phase 3 — Document composer", "Design any target JSON shape from scratch or import the customer's own JSON design, bind objects and arrays to source relations (with suggestions) or group a table's rows into one document, preview and validate it against the design",
             ["OC-047", "OC-048", "OC-049", "OC-059", "OC-060", "OC-061", "OC-062", "OC-063", "OC-064", "OC-065", "OC-066", "OC-067", "OC-069", "OC-070", "OC-071", "OC-075", "OC-076", "OC-077", "OC-081", "OC-083", "OC-145"]),
            ("Phase 4 — Portable runner", "Export a finished mapping as a self-contained bash script and run it in production without the tool; hand customers one Docker image with the app and a bundled single-node Couchbase Server CE",
             ["OC-050", "OC-051", "OC-052", "OC-053", "OC-054", "OC-055", "OC-072", "OC-073", "OC-074", "OC-080"]),
            ("Phase 5 — Harden & ship 2.0", "Upgrade path from 1.0, E2E suite and benchmarks for the new paths, release gate",
             ["OC-078", "OC-057", "OC-056"]),
            ("Phase 6 — Computed fields", "Add any field whose value is fixed, a template or a JavaScript expression over the document's other fields, see it on sample documents, and get the same value in every run, export and target",
             ["OC-136", "OC-137", "OC-138", "OC-139", "OC-140"]),
            ("Phase 7 — Chained embeds", "Embed an embedded object's own relationships from the source side or its foreign-key field, at any depth up to the limit, including parent hierarchies, with shared reads and a size hint",
             ["OC-141", "OC-142", "OC-143", "OC-144"]),
            ("Phase 8 — Trial feedback", "Fixes from a customer trial: real row counts, partial embeds, no lost edits or sign-outs, one entry per related table, and seeing and moving where mappings write",
             ["OC-153", "OC-147", "OC-146", "OC-150", "OC-151", "OC-148", "OC-152", "OC-149"]),
            ("Phase 9 — Editable run settings", "Export without a passphrase or password prompt; run the same script against another environment by editing the connection details in config.json, with passwords kept in base64",
             ["OC-154", "OC-155", "OC-156"]),
            ("Phase 10 — Related tables by direction", "Design & Map lists what a table references and what references it, each with its cardinality, and embeds 1:1 and N:1 as objects and 1:N and M:N as arrays",
             ["OC-157"]),
            ("Phase 11 — Generic runner", "One migrate.sh for every export, reading config.json (where) and mappings.json (what, with a readable outline per mapping); an export is always that folder, with odb optionally included",
             ["OC-158", "OC-159", "OC-160"]),
        ],
        "checklist": [
            "All P0 stories are Implemented (and P1 stories are Implemented or explicitly descoped with sign-off)",
            "CI green on `main`: lint, unit, integration, contract, E2E (including the exported-script runner suite)",
            "OC-056 benchmarks pass every gate on the reference environment; results committed under `bench/results/`",
            "Security review of exported-credential handling (OC-052) and directory path controls (OC-038) completed; no HIGH/CRITICAL vulnerabilities",
            "1.0 → 2.0 upgrade rehearsed on a copy of a 1.0 metadata bucket (OC-057)",
            "Image, all-in-one appliance image and static `odb` binaries signed, SBOM attached, Helm chart published; Couchbase CE redistribution confirmed (OC-072)",
            "User guide, runner guide and deployment docs updated",
            "Security review of the JavaScript sandbox for computed fields (OC-137): forbidden globals, time budget, read-only documents; no HIGH/CRITICAL vulnerabilities",
            "Computed-field (G8) and chained-embed (G9) benchmark gates pass on the reference environment (OC-140, OC-144)",
        ],
    },
    "3.0.0": {
        "theme": ("Couchbase Edge Server becomes the third target kind, next to Couchbase and Directory.", [
            ("Edge Server target", "Configure, test and save an Edge Server connection; migrate into `database.scope.collection` with revision-aware batched writes and verification, re-runs included"),
            ("Setup checks", "Pre-migration checks for writes, users and collections; generate the collection config Edge Server can't create over REST"),
            ("Explore Edge data", "Browse, look up and query the migrated documents in the tool, since Edge Server has no UI"),
            ("Parity", "Same keys and documents as every other target, with E2E tests, a benchmark and a setup guide"),
            ("Optional (P2)", "A managed Edge Server in the all-in-one image whose scopes, collections and users the tool creates"),
        ]),
        "phases": [
            ("Phase 0 — Edge Server spike", "The Edge Server REST questions the target needs (bulk writes, revisions, metadata, limits, licensing) answered against a real server",
             ["OC-084"]),
            ("Phase 1 — Write to Edge Server", "Pick Edge Server as the target, test it, choose or set up its collections, and migrate into it, re-runs included",
             ["OC-085", "OC-086", "OC-087"]),
            ("Phase 2 — Explore Edge data", "Browse, look up and query the migrated documents inside the tool, since Edge Server has no UI",
             ["OC-088"]),
            ("Phase 3 — Harden & ship 3.0", "Same documents as the other targets, benchmark and setup guide; optionally a bundled Edge Server whose collections the tool creates",
             ["OC-089", "OC-090"]),
        ],
        "checklist": [
            "All P0 stories are Implemented (and P1 stories are Implemented or explicitly descoped with sign-off)",
            "CI green on `main`: lint, unit, integration (including the Edge Server container suite), contract, E2E",
            "OC-089 parity test and benchmark gate pass; results committed under `bench/results/edge/`",
            "Edge Server image redistribution confirmed before shipping OC-090 (OC-084)",
            "Edge Server setup guide published and its commands tested",
        ],
    },
    "4.0.0": {
        "theme": ("Oracle comes in: tables become documents, PL/SQL becomes Couchbase UDFs, and both are verified side by side.", [
            ("Oracle source", "19c, 21c and 23ai/26ai over a pure-Go driver (wallets included), with the same discovery, preview, automap, relationship mapping, consistent SCN reads, resume, export and `odb run` as PostgreSQL"),
            ("PL/SQL assessment", "Inventory every function, procedure and package, its dependencies, and how much of it converts automatically"),
            ("Convert to UDFs", "Translate PL/SQL functions into SQL++ or JavaScript UDFs whose embedded SQL reads the migrated documents; review, edit, version and deploy them to Couchbase"),
            ("Side-by-side verification", "Run the PL/SQL on Oracle and its UDF on Couchbase with the same arguments, compare results, and batch-verify saved test cases"),
            ("Beyond functions (P1)", "Procedures and package state; AI-assisted conversion for what the rules can't finish"),
        ]),
        "phases": [
            ("Phase 0 — Spikes & source dialects", "The Oracle driver, test database and Couchbase UDF support confirmed; shared code no longer assumes PostgreSQL, with PostgreSQL output unchanged",
             ["OC-091", "OC-092", "OC-093"]),
            ("Phase 1 — Connect & explore Oracle", "Pick Oracle as the source, test it, discover its schemas with correct types and preview its data",
             ["OC-094", "OC-095", "OC-096", "OC-097"]),
            ("Phase 2 — Map & migrate from Oracle", "Map Oracle tables with every relationship feature and migrate them consistently and resumably, in the app and with the exported runner",
             ["OC-098", "OC-099", "OC-100", "OC-101"]),
            ("Phase 3 — PL/SQL inventory & assessment", "See every PL/SQL unit in the source, what it depends on, and how much of it converts automatically",
             ["OC-102", "OC-103"]),
            ("Phase 4 — Convert to Couchbase UDFs", "Convert PL/SQL functions into inline SQL++ or JavaScript UDFs that read the migrated documents, review and edit them, and deploy them to the target",
             ["OC-104", "OC-105", "OC-106", "OC-107", "OC-108"]),
            ("Phase 5 — Run side by side", "Run a PL/SQL function on Oracle and its UDF on Couchbase with the same arguments, compare results, and verify every function with saved test cases",
             ["OC-109", "OC-110", "OC-111"]),
            ("Phase 6 — Beyond functions", "Convert procedures and simple DML, and optionally ask an AI model to finish the conversions the rules can't",
             ["OC-112", "OC-113"]),
            ("Phase 7 — Harden & ship 4.0", "Oracle parity with PostgreSQL on every target, E2E, benchmarks and guides",
             ["OC-114"]),
        ],
        "checklist": [
            "All P0 stories are Implemented (and P1 stories are Implemented or explicitly descoped with sign-off)",
            "CI green on `main`: lint, unit, integration (including the Oracle Free suite), contract, E2E",
            "OC-114 parity test and benchmark gates pass; results committed under `bench/results/4.0.0/`",
            "PostgreSQL golden-mapping parity unchanged after the dialect refactor (OC-093)",
            "Couchbase UDF support matrix confirmed for Server EE, CE (appliance) and Capella (OC-092)",
            "Security review of wallet handling (OC-094), side-by-side write controls (OC-109) and AI data flow (OC-113); no HIGH/CRITICAL vulnerabilities",
            "Oracle source and PL/SQL conversion guides published and their commands tested",
        ],
    },
    "5.0.0": {
        "theme": ("From one-shot migration to continuous replication, PostgreSQL first.", [
            ("Replicate continuously", "Load every mapped table once, then keep the target in sync with inserts, updates and deletes until cut-over"),
            ("Document-aware engine", "A source-neutral engine traces each changed row to the documents it feeds (embeds and lookup fan-out included) and rebuilds or deletes them on every target kind"),
            ("PostgreSQL change capture", "Logical replication (`pgoutput`) with readiness checks and a setup assistant, a gap-free snapshot handoff, LSN checkpoints, and schema drift, truncate and resync handling"),
            ("Operate safely", "Lag and health monitor, WAL retention guard, alerts, clean stop, and headless `odb replicate` (P1)"),
            ("Proven", "Convergence check, chaos tests, benchmark and replication guide"),
        ]),
        "phases": [
            ("Phase 0 — Replication spike", "Snapshot handoff, re-materialization cost and PostgreSQL logical replication proven on a prototype",
             ["OC-115"]),
            ("Phase 1 — Replication engine", "A long-running replication job that turns row changes into the affected documents and rebuilds or deletes them on every target kind",
             ["OC-116", "OC-117", "OC-118", "OC-119"]),
            ("Phase 2 — PostgreSQL change capture", "Get PostgreSQL ready with a guided checklist, then load and stream its changes with no gap, surviving drift and restarts",
             ["OC-120", "OC-121", "OC-122"]),
            ("Phase 3 — Operate replication", "Monitor lag and health, stay safe on the source's WAL, alert, cut over cleanly, and run headless",
             ["OC-123", "OC-124", "OC-125"]),
            ("Phase 4 — Harden & ship 5.0", "Convergence and chaos testing, benchmarks and the replication guide",
             ["OC-126"]),
        ],
        "checklist": [
            "All P0 stories are Implemented (and P1 stories are Implemented or explicitly descoped with sign-off)",
            "CI green on `main`: lint, unit, integration (PostgreSQL 13 and 17 with `wal_level=logical`), contract, E2E",
            "Convergence check passes for every golden mapping × target kind; nightly chaos suite green three nights in a row (OC-126)",
            "OC-126 benchmark gates pass; results committed under `bench/results/5.0.0/`",
            "WAL retention guard reviewed with a DBA; no tool-created slot can outlive its replication unnoticed",
            "Continuous replication guide published and its commands tested",
        ],
    },
    "6.0.0": {
        "theme": ("Continuous replication for Oracle on the 5.0 engine, with no extra license and no agent on the database host.", [
            ("LogMiner change capture", "A DBA readiness script, then committed changes mined from the redo logs, with an SCN handoff from the initial load"),
            ("Operate", "Stop safely on DDL or missing archive logs, resync, and monitor Oracle-specific health"),
            ("Enterprise topologies (P1)", "Mining on RAC and standby databases"),
            ("External feeds (P2)", "Reuse an existing Debezium or GoldenGate change feed through Kafka"),
            ("Proven", "Convergence and chaos tests on Oracle, benchmark (source CPU included) and guide"),
        ]),
        "phases": [
            ("Phase 0 — LogMiner spike", "Mining from Go, key extraction and the SCN handoff proven against Oracle",
             ["OC-127"]),
            ("Phase 1 — Oracle change capture", "Get Oracle ready with a DBA script, then load and stream committed changes from the redo logs",
             ["OC-128", "OC-129", "OC-130"]),
            ("Phase 2 — Operate Oracle replication", "Stop safely on DDL or missing redo, resync, and monitor Oracle-specific health",
             ["OC-131", "OC-132"]),
            ("Phase 3 — Enterprise topologies & feeds", "RAC and standby mining; reuse an existing Debezium or GoldenGate feed",
             ["OC-133", "OC-134"]),
            ("Phase 4 — Harden & ship 6.0", "Convergence and chaos testing on Oracle, benchmarks and the guide",
             ["OC-135"]),
        ],
        "checklist": [
            "All P0 stories are Implemented (and P1 stories are Implemented or explicitly descoped with sign-off)",
            "CI green on `main`: lint, unit, integration (Oracle Free with archive-log mode), contract, E2E",
            "Convergence check passes for every golden mapping × target kind with an Oracle source; nightly chaos suite green three nights in a row (OC-135)",
            "OC-135 benchmark gates pass, including source CPU; results committed under `bench/results/6.0.0/`",
            "RAC manual test executed and recorded (OC-133) or RAC support explicitly descoped",
            "Oracle replication guide published and its commands tested",
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
        group = [by_id[i] for i in ids if by_id[i]["status"] != "Descoped"]
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
        listed = [by_id[i] for i in ids]
        group = [s for s in listed if s["status"] != "Descoped"]
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
        for n, st in enumerate(listed, 1):
            link = f"[{st['id']}]({st['file']})"
            cells = [link, st["title"], st["priority"], st["epic"], str(st["points"]), st["depends"]]
            if st["status"] == "Descoped":
                cells = [f"~~{c}~~" for c in cells]
                status = "🚫 Descoped"
            elif st["status"] == "Implemented":
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


def render(release: str, every: list) -> str:
    cfg = RELEASES[release]
    prio = priority_info(release)
    stories = [s for s in every if s["status"] != "Descoped"]
    descoped = [s for s in every if s["status"] == "Descoped"]
    total_pts = sum(s["points"] for s in stories)
    by_status = {st: [s for s in stories if s["status"] == st] for st in STATUSES}
    done_pts = sum(s["points"] for s in by_status["Implemented"])

    headline, ships = cfg["theme"]
    out = [f"# Release {release} — Story Index", "",
           "## Theme", "", f"**{headline}**", ""]
    out += [f"- **{area}** — {what}" for area, what in ships] + [""]
    out += ["> Generated by `scripts/update-story-index.py` from each story's header table. "
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
           f"| **Completion** | {pct(len(by_status['Implemented']), len(stories))} | {pct(done_pts, total_pts)} |"]
    if descoped:
        out.append(f"| 🚫 Descoped (not counted above) | {len(descoped)} | {sum(s['points'] for s in descoped)} |")
    out.append("")

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

    out += render_phases(every, cfg["phases"])
    if descoped:
        out += ["## Descoped", "", "Kept for reference; not counted in any total. Each story file says why.", "",
                "| ID | Story | Epic | Points |", "|---|---|---|---|"]
        out += [f"| [{s['id']}]({s['file']}) | {s['title']} | {s['epic']} | {s['points']} |" for s in descoped]
        out.append("")
    out += ["## Release checklist", ""] + [f"- [ ] {item}" for item in cfg["checklist"]] + [""]
    return "\n".join(out)


def main() -> None:
    release_dir = Path(sys.argv[1] if len(sys.argv) > 1 else "docs/release-1.0.0")
    stories = sorted((parse_story(p) for p in release_dir.glob("OC-*.md")), key=lambda s: s["id"])
    if not stories:
        sys.exit(f"no stories in {release_dir}")
    every_id = sorted(int(p.name[3:6]) for p in release_dir.parent.glob("release-*/OC-*.md"))
    if every_id != list(range(every_id[0], every_id[0] + len(every_id))):
        dupes = sorted({i for i in every_id if every_id.count(i) > 1})
        gaps = sorted(set(range(every_id[0], every_id[-1] + 1)) - set(every_id))
        sys.exit(f"story numbering across releases has duplicates {dupes} or gaps {gaps}")
    release = release_dir.name.removeprefix("release-")
    if release not in RELEASES:
        sys.exit(f"no phase configuration for release {release}; add it to RELEASES")
    phased = [i for _, _, ids in RELEASES[release]["phases"] for i in ids]
    if sorted(phased) != [s["id"] for s in stories]:
        sys.exit(f"every story must be in exactly one phase; check RELEASES: {sorted(set(s['id'] for s in stories) ^ set(phased))}")
    release = release_dir.name.removeprefix("release-")
    (release_dir / "INDEX.md").write_text(render(release, stories), encoding="utf-8")
    live = [s for s in stories if s["status"] != "Descoped"]
    print(f"INDEX.md updated: {len(live)} stories, {sum(s['points'] for s in live)} points"
          + (f" ({len(stories) - len(live)} descoped)" if len(live) < len(stories) else ""))


if __name__ == "__main__":
    main()
