# Quick Start Guide - automagic GitLab Automation

Get automagic running in 5 minutes!

## 🚀 Install

```bash
go install github.com/your-username/automagic@latest
```

## ⚙️ Setup

1. **Generate config template**:
```bash
automagic -generate-config
```

2. **Edit `.env` file** with your GitLab credentials:
```bash
# automagic GitLab Automation Configuration
GITLAB_URL=https://gitlab.com
GITLAB_TOKEN=glpat-your-token-here  # Get from GitLab Settings > Access Tokens
GITLAB_USERNAME=your-username

CLAUDE_COMMAND=claude
CLAUDE_FLAGS="--dangerously-skip-permissions --output-format stream-json --verbose"
```

3. **Select your project**:
```bash
automagic -interactive
```

## 🎯 Start Daemon

Choose your mode:

```bash
# Simple mode (recommended for most users)
automagic --daemon

# With persistent sessions
automagic --daemon --memory
```

## 📝 Use It

1. **Add `claude` label** to any GitLab issue
2. **automagic automagically**:
   - Reads issue & comments
   - Creates implementation plan
   - Writes code
   - Creates merge request
   - Marks for human review
3. **Review & comment** on the issue for feedback
4. **automagic automagically** addresses your feedback
5. **Mark as `solved`** when satisfied

## 🏷️ Label Flow

```
claude → picked_up_by_claude → waiting_human_review → (human comment) → picked_up_by_claude → ...
```

## 🔧 Test First

```bash
# See what would happen without doing it
automagic --daemon -dry-run

# Or process a single issue
automagic -issue 123 -dry-run
```

That's it! automagic is now monitoring your GitLab issues. Add the `claude` label to any issue to get started.

## 📚 Need More Help?

See the full [README.md](README.md) for detailed documentation, troubleshooting, and advanced configuration.