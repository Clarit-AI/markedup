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

Alice Chen is a senior AI researcher specializing in knowledge graph construction
and representation learning. She joined the team in early 2023 after completing
her PhD in computational linguistics.

Her current focus is on developing methods for extracting structured knowledge
from unstructured text, with a particular emphasis on maintaining temporal
validity of extracted facts. She collaborates closely with Bob Martinez on
Project Alpha.
