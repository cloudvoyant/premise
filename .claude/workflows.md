# AI Workflow Instructions

## Project Overview

Get project overview from README.md

## Spec-Driven Workflow

Always follow this pattern:

1. Read plan.md first - Understand current phase and tasks
2. Implement in order - Work through tasks sequentially, add sub-checkboxes for complex tasks
3. Mark complete immediately - Check off items as you finish
4. Use TodoWrite within phases - Track progress for user visibility
5. Pause between phases - Inform user when phase completes, wait for confirmation, ask to commit
6. Update docs when done - Sync architecture.md and user-guide.md with reality
7. Delete plan.md - Clean up when complete

See `/upgrade` command for reference implementation of this pattern.

## Phase Completion Workflow

When completing a phase:

1. Mark all tasks complete in `plan.md`
2. Add ✅ to phase heading
3. Inform user: "Phase X complete. Ready to proceed to Phase Y?"
4. WAIT for user confirmation before starting next phase
5. Do not assume user wants to continue immediately

## Git Commits

1. No self attribution
2. Keep it concise and professional
3. Use conventional commits
