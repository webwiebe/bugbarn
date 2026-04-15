# Implementation Plan: [FEATURE]

**Branch**: `[###-feature-name]` | **Date**: [DATE] | **Spec**: [link]

## Summary

[Technical summary]

## Technical Context

**Language/Version**: [e.g. Go 1.23]  
**Primary Dependencies**: [libraries]  
**Storage**: [database/queue/files]  
**Testing**: [test tools]  
**Target Platform**: [runtime targets]  
**Project Type**: [service/web/sdk]  
**Performance Goals**: [goals]  
**Constraints**: [constraints]

## Constitution Check

- [ ] Ingest does not block on transactional storage
- [ ] Unknown payload fields are preserved after scrubbing
- [ ] PII is scrubbed before persistence/UI exposure
- [ ] Low-resource operation is considered
- [ ] Tests are specified for critical behavior

## Project Structure

```text
[tree]
```

## Risk Log

| Risk | Impact | Mitigation |
|------|--------|------------|
| [risk] | [impact] | [mitigation] |

