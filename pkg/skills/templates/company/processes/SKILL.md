---
name: processes
description: "Development workflows, release process, and incident response procedures"
tags: company, process, workflow, release, incident
domain: company
links: org-structure, product-knowledge
---

# Processes

## Development Workflow

1. **Planning** — tickets created from roadmap priorities
2. **Development** — branch from main, implement, write tests
3. **Review** — PR with at least one approval required
4. **QA** — automated tests + manual verification for critical paths
5. **Deploy** — staged rollout: staging → canary → production

## Release Process

- **Cadence**: [Weekly/Biweekly/Continuous]
- **Cut**: [Day/time]
- **Rollback**: [Procedure and decision criteria]

## Incident Response

1. **Detect** — alerts fire, customer reports, monitoring anomalies
2. **Triage** — assess severity (P0-P3), assign incident commander
3. **Mitigate** — restore service; fix root cause later
4. **Communicate** — status page updates, stakeholder notifications
5. **Postmortem** — blameless analysis within 48 hours; action items tracked to completion

See [[org-structure]] for escalation paths and on-call rotations.
