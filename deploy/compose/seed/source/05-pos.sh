#!/usr/bin/env bash
# The design-first fixtures (OC-162, OC-171): the pos, crm and support schemas, from the
# backend's fixture package (mounted at /seed/pos by dev.yml). Skipped where it isn't mounted.
set -euo pipefail
if [ -f /seed/pos/schema.sql ]; then
  psql -v ON_ERROR_STOP=1 -q --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" -f /seed/pos/schema.sql
fi
