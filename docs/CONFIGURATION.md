# Configuration

AGX stores config and its SQLite database under:

```text
~/.config/agx/
```

Project config can override global settings from:

```text
<project-root>/.agx/config.toml
```

Do not commit local config files if they contain API keys, bot tokens, private
paths, or other sensitive values.

## Global Config

Example:

```toml
default_agent = "claude"

[agents.local]
command = "my-agent-cli"
args = ["--auto"]
resume_args = ["resume", "--last"]
print_args = ["print"]
env = { MY_API_KEY = "..." }
description = "Local custom agent"
```

## Project Config

Project config can override the global default and add or override agents:

```toml
# <project-root>/.agx/config.toml
default_agent = "codex"

[agents.codex-fast]
command = "codex"
args = ["--full-auto", "--model", "fast"]
```

## Worktrees

Enable per-task git worktrees:

```toml
[worktree]
enabled = true
# Optional. Defaults to the current branch when the task starts.
base_branch = "main"
```

When worktrees are enabled, AGX creates:

- worktree: `<project-root>/.agx/worktrees/task-<short-id>`
- branch: `agx/task-<short-id>`

Use worktrees when you want each task to edit in isolation. Use the project
checkout when you want agents to operate directly in the current working tree.

