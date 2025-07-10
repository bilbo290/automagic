# Quick Start Guide - automatic GitLab Automation

Get automatic running in 5 minutes!

## 🚀 Install

```bash
go install github.com/your-username/automatic@latest
```

## ⚙️ Setup

1. **Generate config template**:
```bash
automatic -generate-config
```

2. **Edit `.env` file** with your GitLab credentials:
```bash
# automatic GitLab Automation Configuration
GITLAB_URL=https://gitlab.com
GITLAB_TOKEN=glpat-your-token-here  # Get from GitLab Settings > Access Tokens
GITLAB_USERNAME=your-username

CLAUDE_COMMAND=claude
CLAUDE_FLAGS="--dangerously-skip-permissions --output-format stream-json --verbose"
```

3. **Select your project**:
```bash
automatic -interactive
```

## 🎯 Start Daemon

Choose your mode:

```bash
# Simple mode (recommended for most users)
automatic --daemon

# With persistent sessions
automatic --daemon --memory
```

## 📝 Use It

1. **Add `claude` label** to any GitLab issue
2. **automatic automatically**:
   - Reads issue & comments
   - Creates implementation plan
   - Writes code
   - Creates merge request
   - Marks for human review
3. **Review & comment** on the issue for feedback
4. **automatic automatically** addresses your feedback
5. **Mark as `solved`** when satisfied

## 🏷️ Label Flow

```
claude → picked_up_by_claude → waiting_human_review → (human comment) → picked_up_by_claude → ...
```

## 🔧 Test First

```bash
# See what would happen without doing it
automatic --daemon -dry-run

# Or process a single issue
automatic -issue 123 -dry-run
```

That's it! automatic is now monitoring your GitLab issues. Add the `claude` label to any issue to get started.

## 📚 Need More Help?

See the full [README.md](README.md) for detailed documentation, troubleshooting, and advanced configuration.