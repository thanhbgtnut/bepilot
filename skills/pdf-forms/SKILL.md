---
name: PDF Forms
description: Inspect, fill, and flatten AcroForm PDF forms. Use whenever the user wants to complete a fillable PDF, extract the list of form fields from a PDF, merge data into a PDF template, or produce a non-editable (flattened) copy of a completed form.
allowed_tools:
  - http_fetch
---

# PDF Forms

A procedure for working with fillable PDF forms (AcroForm).

## When this applies

- "Fill in this PDF form with the following details…"
- "What fields does this PDF have?"
- "Flatten this form so it can't be edited."
- "Merge this CSV row into the template PDF."

## Steps

1. **Obtain the file.** If given a URL, fetch it with `http_fetch`. If given a
   path or upload reference, note it for the caller to supply bytes.
2. **Enumerate fields.** List every field with its type (text, checkbox, radio,
   choice), current value, and whether it is required. Present this as a table
   before filling anything.
3. **Map the user's data to field names.** Ask for clarification only when a
   value is genuinely ambiguous; otherwise proceed and report your mapping.
4. **Fill.** Write each value using the exact field name. For checkboxes use the
   field's real "on" state, not a literal "true".
5. **Validate.** Re-read the filled document and confirm each target field holds
   the intended value.
6. **Flatten if asked.** Produce a copy with form fields rendered as static
   content so the result cannot be changed.

## Output

Return: the field table, the value mapping you used, and a summary of what was
written. If a value could not be placed, say which and why.

## Notes

- Never invent field names — only use names that exist in the document.
- Preserve pages, annotations, and digital signatures that are not part of the
  form.
- See `reference/field-types.md` for the checkbox/radio value rules.
