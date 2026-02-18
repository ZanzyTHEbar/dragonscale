---
name: onboarding
description: "New hire onboarding — access, environment setup, first-week guide"
tags: company, onboarding, setup, new-hire
domain: company
links: org-structure, processes, product-knowledge
---

# Onboarding

## Day 1

- [ ] Get laptop and credentials
- [ ] Set up email, Slack/Teams, calendar
- [ ] Request access: GitHub, CI/CD, cloud console, monitoring dashboards
- [ ] Clone main repositories and verify build

## Week 1

- [ ] Read [[product-knowledge]] to understand what we build and why
- [ ] Read [[org-structure]] to understand teams and reporting
- [ ] Read [[processes]] to understand how we ship
- [ ] Complete a "good first issue" to practice the full workflow
- [ ] Shadow an on-call shift to understand production

## Environment Setup

```bash
# Clone and build (customize for your stack)
git clone <repo-url>
cd <repo>
make setup    # installs dependencies
make build    # verifies compilation
make test     # runs test suite
```

## Key Contacts

| Role | Name | When to reach out |
|------|------|-------------------|
| Buddy | [Assigned] | Any question, no matter how small |
| Team Lead | [Name] | Technical direction, priorities |
| HR | [Name] | Benefits, policies, admin |
