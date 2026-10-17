# Feature Specification: [FEATURE NAME]

**Feature Branch**: `[###-feature-name]`  
**Created**: [DATE]  
**Status**: Draft | Shipped | Partial | Deferred | Superseded  
**Last verified against the tree**: [DATE] at `[short sha]`  
**Issues**: #[epic], #[issue]  
**Principles touched**: [constitution numerals]  
**Input**: User description: "$ARGUMENTS"

## User Scenarios & Testing

### User Story 1 - [Title] (Priority: P1)

[Describe the user journey in plain language]

**Why this priority**: [Value/risk]

**Independent Test**: [How to verify this story independently]

**Acceptance Scenarios**:

1. **Given** [state], **When** [action], **Then** [outcome]

## Requirements

### Functional Requirements

- **FR-001**: The system MUST [behaviour]. Issues: [#123](full-url). Tests: `path::test_name`.
  A requirement with neither is marked `[inferred]`; one the code does not meet yet is marked `[gap]` with an issue.

### Non-Functional Requirements

- **NFR-001**: [Requirement]

### Key Entities

- **[Entity]**: [Description]

## Success Criteria

- **SC-001**: [Measurable outcome]

## Assumptions

- [Assumption]

## Drift

Where a document or issue disagrees with the tree. The tree is the fact. An empty
table is valid; write `not established` when the tree cannot answer.

| Claim | Source | Tree |
|---|---|---|
| [what the document says] | [document or issue] | [what the code does, with the path] |

## Proof contract

- [ ] [Observable outcome, with its evidence: a screenshot, an e2e run, or `path::test_name`.]

## Sources

- [docs, ADRs, issues, test files and code paths read to write this spec]
