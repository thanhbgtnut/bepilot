---
name: Tabular Data Cleanup
description: Clean and normalize messy tabular data (CSV, TSV, spreadsheet exports). Use when the user wants to deduplicate rows, fix inconsistent categories or casing, parse and standardize dates or phone numbers, handle missing values, split or merge columns, or profile a dataset for quality issues.
---

# Tabular Data Cleanup

A repeatable procedure for turning a messy table into a tidy one.

## When this applies

- "Clean up this CSV."
- "These category names are inconsistent — normalize them."
- "Dedupe this list by email."
- "Why is this dataset weird?" (profiling)

## Steps

1. **Profile first.** Report row/column counts, per-column type inference, null
   counts, cardinality, and 3–5 example values per column. Do not change
   anything yet.
2. **Agree the target schema.** State the intended type and format for each
   column (e.g. `signup_date` → ISO-8601 date). Flag columns you would drop.
3. **Normalize values.**
   - Trim whitespace; collapse internal runs of spaces.
   - Standardize casing for categoricals; map near-duplicate labels to one
     canonical form and list every mapping you applied.
   - Parse dates/numbers/phones to a single format; keep the original in a
     `*_raw` column when the parse is lossy or uncertain.
4. **Handle missing data.** Choose per column: leave null, impute (say how), or
   drop the row. Never silently fill.
5. **Deduplicate.** State the key. Keep the most complete / most recent record;
   report how many rows were removed.
6. **Validate.** Re-profile and diff against step 1. Confirm no unintended
   changes to untouched columns.

## Output

Return: the before/after profile, the full list of transformations (with the
category mappings), the row counts removed or changed, and the cleaned table.

## Notes

- Determinism matters: the same input must always produce the same output.
- Preserve a way back — keep raw columns whenever a transformation loses info.
