---
applyTo: ".github/skills/**/*.md"
excludeAgent: "code-review"
---

Project skills under `.github/skills/` are part of the repository's Copilot customization surface. Keep them aligned with the actual repository structure, commands, and docs rather than drifting into generic guidance.

If you update a skill, make sure its examples and validation steps still match current CI and repository workflows.

Avoid duplicating the same instructions across multiple skills unless the overlap is intentional. Prefer keeping each skill scoped to its area: general development, mesh/protobuf, or operations.

When editing these files during an active Copilot CLI session, remember that the CLI may need `/skills reload` before the updated skill definitions are picked up.
