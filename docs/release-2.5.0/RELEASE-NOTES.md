# Omni-Data-Bus 2.5.0 — release notes

**Start from the customer's document.** Import their JSON design, and the tool proposes the whole mapping from your connected source. Then review it on the customer's own structure and keep what's right.

## New

- **The whole mapping proposed from a design** (OC-163–OC-167). From a JSON Schema or sample documents, the proposal picks:
  - the driving table;
  - the related table behind every object and array, in either direction and at any depth;
  - what fills each field: a column (also from a one-to-one or many-to-one table next to it), a type conversion (dollars into whole cents, numbers into display text), a constant, a combined name, a total over child rows, or a value decided by a one-to-one table.

  It reads what the design says: constants, allowed values, "sourced from table.column", "Not populated", and the document ID pattern. Each proposal has a confidence and its reasons.
- **Review mode** (OC-168, OC-169). The proposal on the customer's tree:
  - with confidence chips, filters (*Needs review*, *Unresolved*, *Not populated*, *Edited*) and `n` / `p`;
  - fine-tuned with the usual editors;
  - applied in one undo step (or *Apply high only*, or *Discard*).

  An unresolved field says why and offers its fix, and a coverage sheet can go to the customer.
- **New mapping from a design…** on the Mappings page. When the table is already mapped, you're asked first, and *Update this mapping* keeps everything that's mapped (OC-170).
- **Variant shapes** (OC-171). Arrays whose items take one of several shapes (`oneOf`, or one design file per kind), each built from the table its row belongs to: by which key is set (an exclusive arc, read from CHECK constraints), by a type column, or as a union of tables. Designs split across files are imported together (several files or a folder).
- **Canvas: both ends in view** (OC-174, OC-175).
  - Clicking a source column scrolls to the field it fills. Click again for its next field (*1 of 3*).
  - Clicking a field scrolls to its column.
  - Source columns follow the document, object by object, with unmapped ones under *Not mapped*. *Order: Table* brings back the old order.
- **Issues point at their field** (OC-176). Each issue shows its field's name in green. Clicking the issue opens the Canvas, scrolls to the field in the target pane and flashes it.
- **Unused lookups** (OC-177).
  - A "values only" table that no field reads is reported (`LOOKUP_UNUSED`), with *Remove lookup*.
  - The Canvas folds it to its header.
  - The bind dialog warns before adding a lookup of a table the object already reads.
- **The wheel shows how much of the document is done** (OC-178). On the Mappings page and in the mapping:
  - the wheel shows the target fields that are filled, or deliberately left empty, out of all of them (*38 of 40 fields done*);
  - mapping a field or marking it **not populated**, now possible in the Canvas and the Composer, moves it;
  - the auto-map figures stay as detail under the headline.
  - the wheel separates the fields **filled with data** (solid) from those **left empty on purpose** (lighter), so *98% done* can't hide a half-empty document. The line under the headline reads *73 filled with data · 25 not populated · 2 to go (1 required)*, and one *Source columns used* figure covers the table and its embedded tables (OC-187).
- **The current date and time as a field's value** (OC-179). **Value source → Date** fills a field such as `updatedAt`:
  - with the run's start or each document's write time;
  - as epoch ms or seconds, ISO 8601, a date or a pattern, or the same format as the document's other dates.

  A format that doesn't suit the field is flagged, with a fix.
- **Run an export with the `odb` command alone, on macOS, Windows or Linux** (OC-180–OC-186).
  - An export is now `config.json`, `mappings.json` and `README.md`, with no script and nothing OS-specific: test it on your laptop, then copy the same folder to the server.
  - In the folder, `odb preflight`, `odb run`, `odb resume`, `odb status`, `odb verify` and `odb inspect` find their files themselves, keep the exit codes, and print readable progress on a terminal (JSON lines when piped).
  - odb is one file to install: one signed and notarized binary for every Mac, a signed `odb.exe` for Windows, static Linux binaries. The README lists each download with its checksum. **Add odb for a machine without internet access** puts one in the export's `bin/`.
  - With Docker, the README's one command runs the same commands, and odb says when a target folder isn't mounted or `localhost` means the container.
  - **Any machine's odb, from any server** (OC-188). The server gets a machine's odb from the release the first time an export needs it, verifies it against the checksums built into the image, and caches it. Air-gapped servers use a mounted dist folder. The dialog says, for each machine, whether it's available, downloaded on first use, or why not.
- **Release and build shown clearly** (OC-172). Every page, every export header and `odb --version` show the release from the `VERSION` file and the build: `Omni Data Bus 2.5.0 · build 7f6cea2+418`. A compact `v2.5.0` stays visible while a page is maximized.

- **Open source under Apache-2.0** (OC-189).
  - Copyright 2026 Couchbase, Inc.; author: Anuj Sahni. `LICENSE`, `NOTICE` and `THIRD_PARTY_NOTICES.md` are at the repository root.
  - They're also in the image (`/usr/share/doc/omni-data-bus/`), in every odb download, in an export that includes odb (`bin/LICENSES/`), and in the app (*Settings › About › Open-source licences*).
  - `odb --version` names the licence. CI fails if a shipped dependency's licence isn't allowed.
  - The all-in-one image is labeled `Apache-2.0 AND LicenseRef-Couchbase-CE`, because it bundles Couchbase Server CE.

## Changes

- **Exports no longer include `migrate.sh`** (OC-182). The export dialog's odb checkbox is unchecked by default, and a server without release downloads no longer adds its own odb to every export.
- **Directory-target folder names** that Windows can't create (`con`, `aux.json`, a trailing `.`) are refused on every OS, so an export runs the same everywhere (OC-186).

- Discovery reports each table's CHECK constraints and exclusive arcs.
- A field can be marked **not populated**: nothing is written for it, it counts as done, and an object whose every field is not populated is written as `null`.
- JSON Schema `enum` is imported, exported back and checked in the preview (`REFERENCE_ENUM_VALUE`).
- A lookup must read one row: a lookup on columns that aren't a key of its table is `LOOKUP_NOT_UNIQUE`.
- A field filled by two mappings (`DUPLICATE_TARGET`) now names both: *from customers.user_id and from carts.customer_id*, not internal mapping ids. *Keep …* removes the other mapping.
- The Canvas counts a column an expression reads (`concat(row.first_name, ' ', row.last_name)`) as used: green dot and a line.
- The job pre-flight warns when two mappings of a run write the same keys into the same place (`KEY_OVERLAP`).
- Binding suggestions (OC-060) score cardinality adapters more finely: an object over one-to-many 0.7, an array over many-to-one 0.6.

## Compatibility

- **2.0 exports keep working.**
  - Their `migrate.sh` runs odb 2.0, as before.
  - odb 2.5 also runs a 2.0 export folder directly (`odb preflight`, `odb run` in it).
  - `odb run --mappings … --config …` and `odb run preflight|status|verify` are unchanged.
  - On a terminal, odb now prints text unless `--log-format json` is set; piped output stays JSON.

- 2.0 bundles run on 2.5 `odb`. Bundles exported by 2.5 need `odb` 2.5 or later, as every minor release's bundles do.
- Jobs whose mappings use variant shapes need 2.5 workers. During a rolling upgrade, 2.0 workers leave them alone (`minWorkerVersion 2.5.0`).
- The API only adds fields and routes:
  - `POST …/mappings/{id}/propose` and `POST …/mappings/propose`;
  - `files` on `POST /doc-schemas/infer`;
  - `build` on `GET /version`;
  - new optional fields on designs, relations, field mappings, tables and summaries.

## Quality gate

`make propose-score` scores the proposal on the point-of-sale fixture against its expected mapping, and `make bench-gate RELEASE=2.5.0` judges gate G10. On 2.5.0: driving table, bindings and every field category at 100 %, and no wrong high-confidence proposal. 2.0 got 36 % of the bindings, 0–74 % of the field categories, and 5 wrong high-confidence fields.
