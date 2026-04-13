# Schema Reference -- YAML Frontmatter Specification

## Overview

Every markedup file is a standard Markdown document with a YAML frontmatter block at the top, delimited by `---`. The frontmatter defines a **node** in the knowledge graph: its identity, classification, relationships to other nodes, temporal metadata, and provenance.

The frontmatter is parsed by `markdown.ParseBytes`, which extracts the YAML block using a regex matching `---\n...\n---` delimiters, then unmarshals it into a `schema.GraphFrontmatter` struct. Validation is performed separately by `schema.ValidatePage`.

```yaml
---
id: my-page
title: My Page
entity-type: concept
confidence: 0.9
---

Body content goes here.
```

---

## Required Fields

These fields must be present and non-empty. Omitting any of them causes a validation error.

| YAML Key      | Go Field     | Type     | Description                                |
|---------------|--------------|----------|--------------------------------------------|
| `id`          | `ID`         | `string` | Unique identifier for this node            |
| `title`       | `Title`      | `string` | Human-readable display title               |
| `entity-type` | `EntityType` | `string` | Classification of this node (free-form)    |
| `confidence`  | `Confidence` | `float64`| Confidence score for this node's accuracy  |

### Validation Rules (Required Fields)

- **`id`**: Must be a non-empty string. No format constraints beyond that.
- **`title`**: Must be a non-empty string.
- **`entity-type`**: Must be a non-empty string. There is no fixed set of allowed values -- any non-empty string is accepted (e.g., `person`, `project`, `concept`, `event`).
- **`confidence`**: Must be in the range `[0.0, 1.0]` inclusive. Values outside this range produce a validation error. Note: a zero value (the Go default for `float64`) is valid, so `confidence` is technically not enforced as "required" at the validation level -- only `id`, `title`, and `entity-type` have explicit empty-string checks.

---

## Optional Fields

These fields default to their Go zero values when omitted (empty string, nil slice, zero struct, etc.).

| YAML Key             | Go Field           | Type               | Description                                         |
|----------------------|--------------------|--------------------|-----------------------------------------------------|
| `tags`               | `Tags`             | `[]string`         | Free-form labels for categorization and search      |
| `entities`           | `Entities`         | `[]Entity`         | Named entities referenced within this page          |
| `relationships`      | `Relationships`    | `[]Relationship`   | Directed edges to other nodes in the graph          |
| `temporal`           | `Temporal`         | `TemporalInfo`     | Time-based metadata for confidence decay            |
| `provenance`         | `Provenance`       | `Provenance`       | Origin and authorship information                   |
| `semantic-hints`     | `SemanticHints`    | `[]string`         | Free-text phrases to aid semantic search             |
| `possible-questions` | `PossibleQuestions` | `[]string`        | Questions this page could answer                    |

---

## Field Reference

### `id`

- **YAML key**: `id`
- **Type**: `string`
- **Required**: Yes
- **Validation**: Must be non-empty.
- **Example**: `"alice"`, `"project-alpha"`, `"concept-graph"`

The `id` field uniquely identifies this node across the knowledge graph. It is used as the `target` value in relationship references from other pages.

### `title`

- **YAML key**: `title`
- **Type**: `string`
- **Required**: Yes
- **Validation**: Must be non-empty.
- **Example**: `"Alice Chen"`, `"Project Alpha"`, `"Knowledge Graphs"`

The `title` is the human-readable name of this node. When serializing a page, markedup generates an H1 heading (`# Title`) at the top of the body content. When parsing, a leading H1 matching the title is automatically stripped to avoid duplication.

### `entity-type`

- **YAML key**: `entity-type`
- **Type**: `string`
- **Required**: Yes
- **Validation**: Must be non-empty. Free-form -- no fixed set of allowed values.
- **Example**: `"person"`, `"project"`, `"concept"`, `"event"`

Classifies this node for filtering and search. Common conventions include `person`, `project`, `concept`, and `event`, but any non-empty string is accepted.

### `confidence`

- **YAML key**: `confidence`
- **Type**: `float64`
- **Required**: Effectively yes (but defaults to `0.0` if omitted)
- **Validation**: Must be in `[0.0, 1.0]`.
- **Example**: `0.85`, `0.92`, `0.95`

Represents how confident you are in the accuracy or relevance of this page. Used by the temporal decay system to model how confidence degrades over time based on the `decay-rate`.

### `tags`

- **YAML key**: `tags`
- **Type**: `[]string`
- **Required**: No
- **Validation**: None
- **Example**: `[person, engineer, ai-researcher]`

A list of free-form labels used for categorization, filtering, and search. Tags are compatible with Obsidian's tag system (see [Obsidian Compatibility](#obsidian-compatibility)).

### `entities`

- **YAML key**: `entities`
- **Type**: `[]Entity` (see [Entity Object](#entity-object))
- **Required**: No
- **Validation**: Each entity must have a non-empty `name` field.
- **Example**:
  ```yaml
  entities:
    - name: Alice Chen
      aliases: [alice, Dr. Chen]
      role: researcher
  ```

A list of named entities referenced within this page. Entities support aliases for fuzzy matching when the same concept appears under different names.

### `relationships`

- **YAML key**: `relationships`
- **Type**: `[]Relationship` (see [Relationship Object](#relationship-object))
- **Required**: No
- **Validation**: Each relationship must have non-empty `target` and `type`, and `strength` must be in `[0.0, 1.0]`.
- **Example**:
  ```yaml
  relationships:
    - target: bob
      type: colleague
      strength: 0.9
  ```

Directed edges from this page to other nodes in the knowledge graph. The `target` value should match the `id` of the target page. The serializer auto-generates a `## Related` section with `[[wikilinks]]` from these relationships.

### `temporal`

- **YAML key**: `temporal`
- **Type**: `TemporalInfo` (see [Temporal Object](#temporal-object))
- **Required**: No
- **Validation**: Date fields, if present, must match `YYYY-MM-DD` format. `decay-rate` must be non-negative.
- **Example**:
  ```yaml
  temporal:
    valid-from: "2023-01-01"
    last-verified: "2026-04-01"
    decay-rate: 0.01
  ```

Time-based metadata used for confidence decay calculations. Controls how quickly information is considered stale.

### `provenance`

- **YAML key**: `provenance`
- **Type**: `Provenance` (see [Provenance Object](#provenance-object))
- **Required**: No
- **Validation**: None
- **Example**:
  ```yaml
  provenance:
    sources: ["hr-directory", "research-portal"]
    created-by: system
  ```

Tracks where the information in this page originated and who created it.

### `semantic-hints`

- **YAML key**: `semantic-hints`
- **Type**: `[]string`
- **Required**: No
- **Validation**: None
- **Example**: `["AI researcher", "knowledge graph expert"]`

Free-text phrases that provide additional semantic context for search and embedding. These hints help the search pipeline understand what this page is about beyond the title and tags.

### `possible-questions`

- **YAML key**: `possible-questions`
- **Type**: `[]string`
- **Required**: No
- **Validation**: None
- **Example**: `["Who is Alice?", "What does Alice study?"]`

Questions that this page could answer. Used by the search pipeline to improve question-answering relevance.

---

## Nested Object Types

### Entity Object

Defined in `schema.Entity`. Represents a named entity referenced within a page.

| YAML Key  | Go Field  | Type       | Required | Description                                    |
|-----------|-----------|------------|----------|------------------------------------------------|
| `name`    | `Name`    | `string`   | Yes      | Primary name of the entity                     |
| `aliases` | `Aliases` | `[]string` | No       | Alternative names for fuzzy matching           |
| `role`    | `Role`    | `string`   | No       | The entity's role within this page's context   |

**Validation**: `name` must be non-empty. No validation is applied to `aliases` or `role`.

**Example**:
```yaml
entities:
  - name: Knowledge Graph
    aliases: [KG, knowledge graph]
    role: subject
  - name: Alice Chen
    aliases: [alice, Dr. Chen]
    role: researcher
```

### Relationship Object

Defined in `schema.Relationship`. Represents a directed edge from this page to a target page.

| YAML Key   | Go Field   | Type      | Required | Description                                  |
|------------|------------|-----------|----------|----------------------------------------------|
| `target`   | `Target`   | `string`  | Yes      | The `id` of the target page                  |
| `type`     | `Type`     | `string`  | Yes      | Label describing the relationship            |
| `strength` | `Strength` | `float64` | Yes      | Edge weight in `[0.0, 1.0]`                  |

**Validation**:
- `target`: Must be non-empty.
- `type`: Must be non-empty. Free-form -- common values include `colleague`, `member`, `studies`, `introduced-at`.
- `strength`: Must be in the range `[0.0, 1.0]`.

**Example**:
```yaml
relationships:
  - target: bob
    type: colleague
    strength: 0.9
  - target: concept-graph
    type: studies
    strength: 0.8
```

### Temporal Object

Defined in `schema.TemporalInfo`. Captures time-based metadata for confidence decay calculations.

| YAML Key        | Go Field       | Type      | Required | Description                                      |
|-----------------|----------------|-----------|----------|--------------------------------------------------|
| `valid-from`    | `ValidFrom`    | `string`  | No       | Date from which this information is valid         |
| `valid-until`   | `ValidUntil`   | `string`  | No       | Date after which this information expires         |
| `last-verified` | `LastVerified`  | `string`  | No       | Date when this information was last confirmed     |
| `decay-rate`    | `DecayRate`    | `float64` | No       | Rate at which confidence degrades over time       |

**Validation**:
- `valid-from`, `valid-until`, `last-verified`: If present, must match the `YYYY-MM-DD` format (regex: `^\d{4}-(0[1-9]|1[0-2])-(0[1-9]|[12]\d|3[01])$`).
- `decay-rate`: Must be non-negative (>= 0). There is no upper bound.

**Example**:
```yaml
temporal:
  valid-from: "2023-01-01"
  valid-until: "2025-12-31"
  last-verified: "2026-04-01"
  decay-rate: 0.01
```

Date values should be quoted strings in YAML to avoid parser ambiguity.

### Provenance Object

Defined in `schema.Provenance`. Tracks information origin and authorship.

| YAML Key     | Go Field    | Type       | Required | Description                             |
|--------------|-------------|------------|----------|-----------------------------------------|
| `sources`    | `Sources`   | `[]string` | No       | Where this information came from        |
| `created-by` | `CreatedBy` | `string`   | No       | Who or what created this page           |

**Validation**: None. Both fields are optional and have no format constraints.

**Example**:
```yaml
provenance:
  sources: ["hr-directory", "research-portal"]
  created-by: system
```

---

## Complete Example

The following frontmatter uses every available field:

```yaml
---
id: alice
title: Alice Chen
entity-type: person
confidence: 0.95
tags: [person, engineer, ai-researcher]
entities:
  - name: Alice Chen
    aliases: [alice, Dr. Chen]
    role: researcher
relationships:
  - target: bob
    type: colleague
    strength: 0.9
  - target: concept-graph
    type: studies
    strength: 0.8
temporal:
  valid-from: "2023-01-01"
  last-verified: "2026-04-01"
  decay-rate: 0.01
provenance:
  sources: ["hr-directory", "research-portal"]
  created-by: system
semantic-hints: ["AI researcher", "knowledge graph expert"]
possible-questions: ["Who is Alice?", "What does Alice study?"]
---

Page body content goes here.
```

## Minimal Example

The following frontmatter uses only the required fields:

```yaml
---
id: my-note
title: My Note
entity-type: concept
confidence: 0.8
---

A minimal knowledge graph node.
```

---

## Obsidian Compatibility

markedup is designed to work seamlessly with [Obsidian](https://obsidian.md/) vaults.

### Wikilinks

When a page is serialized (written to disk), markedup auto-generates a `## Related` section at the bottom of the file. Each relationship is rendered as an Obsidian-compatible `[[wikilink]]`:

```markdown
## Related

- [[bob]] (type: colleague, strength: 0.9)
- [[concept-graph]] (type: studies, strength: 0.8)
```

The `[[target]]` values correspond to the `target` field in relationships, which should match the `id` of the linked page. Obsidian resolves these wikilinks to files with matching names.

When parsing, markedup strips any existing `## Related` section before re-generating it, preventing duplication on round-trip read-write cycles.

### Tags

The `tags` array in frontmatter is compatible with Obsidian's property-based tag system. Obsidian reads YAML frontmatter tags and displays them in the tag pane, enabling cross-file navigation:

```yaml
tags: [person, engineer, ai-researcher]
```

These appear in Obsidian's tag search and can be used for filtering across the vault.

### Title and H1

markedup generates an `# Title` heading at the top of the body when serializing. When parsing, if the body starts with an H1 heading that matches the frontmatter `title`, it is stripped to avoid duplication. This means you can open markedup files in Obsidian and see a clean title without duplicated headings.

---

## Validation Summary

The following table summarizes all validation rules enforced by `schema.ValidatePage`:

| Rule               | Fields Affected                                        | Constraint                          |
|--------------------|--------------------------------------------------------|-------------------------------------|
| Required fields    | `id`, `title`, `entity-type`                           | Must be non-empty strings           |
| Confidence range   | `confidence`                                           | Must be in `[0.0, 1.0]`            |
| Relationship rules | `relationships[].target`, `relationships[].type`       | Must be non-empty strings           |
| Relationship strength | `relationships[].strength`                          | Must be in `[0.0, 1.0]`            |
| Temporal dates     | `temporal.valid-from`, `temporal.valid-until`, `temporal.last-verified` | If present, must be `YYYY-MM-DD` |
| Entity names       | `entities[].name`                                      | Must be non-empty strings           |
| Decay rate         | `temporal.decay-rate`                                  | Must be non-negative (>= 0)        |

Validation errors include the field path (e.g., `relationships[0].strength`), a human-readable message, and a suggested fix.
