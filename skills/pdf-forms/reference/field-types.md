# AcroForm field types

| Type      | How to set the value                                              |
|-----------|------------------------------------------------------------------|
| Text      | Plain string. Respect `MaxLen` if present.                       |
| Checkbox  | Use the export value from the widget's `/AP` `/N` dictionary (often `Yes`, `On`, or a custom name). `Off` unchecks. |
| Radio     | Set the parent field to the export value of the chosen kid.      |
| Choice    | One of the entries in the `/Opt` array (list or combo box).      |
| Signature | Do not fill programmatically. Leave for a signing step.          |

## Common mistakes

- Writing `"true"` / `"false"` into a checkbox instead of its export value.
- Setting a radio kid directly rather than the parent field.
- Ignoring `/Ff` flags such as read-only (bit 1) or required (bit 2).
