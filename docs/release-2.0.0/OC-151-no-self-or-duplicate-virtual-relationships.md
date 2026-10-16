# OC-151 — No self-joins or duplicates in virtual relationships

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E9 Relationship Mapping | P1 | 2 | OC-042 | Implemented |

## User story
As a **data engineer** embedding related tables, I want each related table listed once, so that I'm not left choosing between four identical "Order Details via order_id" rows.

## Background (2026-10-15)
Virtual relationships could join a table to itself on the same columns (`Order Details.order_id → Order Details.order_id`), and the same relationship could be saved twice. Each one adds a ↗ and a ↙ row to Related tables, so two duplicates showed the same option four times. Such a join only matches each row to itself, so it has no use.

## Scope
**In:**
- Creating or editing a virtual relationship that joins a table to itself on the same columns is refused (422): "This joins each row of … to itself. A self-reference uses different columns on each side, e.g. manager_id → employee_id." Self-references on different columns stay allowed.
- A relationship the project already has (same ends and columns, either direction) is refused (409): "This relationship already exists as …".
- The relationship graph skips same-column self-hops and repeats of a hop already there, so existing bad relationships stop showing at once; a virtual copy of a foreign key gives way to the foreign key.

**Out:** deleting existing bad relationships automatically (they stay listed in the virtual relationships dialog, with no effect).

## Acceptance criteria & unit tests

### AC1 — Refused on save
**Given** a same-column self-join, **then** saving it is 422. **Given** an existing relationship, **then** saving it again, or reversed, is 409.

### AC2 — Listed once
**Given** two identical relationships, a same-column self-join and a virtual copy of a foreign key, **then** the graph has one hop per distinct relationship, and the foreign key's.

| Test | Location | Verifies |
|---|---|---|
| `TestRelationshipsAPI_RejectsSelfAndDuplicates` | `backend/internal/relations/handlers_test.go` | AC1 |
| `TestBuild_SkipsSelfAndDuplicateHops` | `backend/internal/relations/graph_test.go` | AC2 |
