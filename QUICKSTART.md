# Quick Start Guide - Peter GitLab Automation

Get Peter running in 5 minutes!

## 🚀 Install

```bash
go install github.com/your-username/peter@latest
```

## ⚙️ Setup

1. **Create `peter.yaml`**:
```yaml
gitlab:
  url: "https://gitlab.com"
  token: "glpat-your-token-here"  # Get from GitLab Settings > Access Tokens
  username: "your-username"

claude:
  command: "claude"
  flags: "--dangerously-skip-permissions --output-format stream-json --verbose"
```

2. **Select your project**:
```bash
peter -interactive
```

## 🎯 Start Daemon

Choose your mode:

```bash
# Simple mode (recommended for most users)
peter --daemon

# With persistent sessions
peter --daemon --memory
```

## 📝 Use It

1. **Add `claude` label** to any GitLab issue
2. **Peter automatically**:
   - Reads issue & comments
   - Creates implementation plan
   - Writes code
   - Creates merge request
   - Marks for human review
3. **Review & comment** on the issue for feedback
4. **Peter automatically** addresses your feedback
5. **Mark as `solved`** when satisfied

## 🏷️ Label Flow

```
claude → picked_up_by_claude → waiting_human_review → (human comment) → picked_up_by_claude → ...
```

## 🔧 Test First

```bash
# See what would happen without doing it
peter --daemon -dry-run

# Or process a single issue
peter -issue 123 -dry-run
```

That's it! Peter is now monitoring your GitLab issues. Add the `claude` label to any issue to get started.

## 📚 Need More Help?

See the full [README.md](README.md) for detailed documentation, troubleshooting, and advanced configuration.