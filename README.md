# automagic - GitLab Issue Automation with Claude AI

automagic is a powerful daemon that automates GitLab issue processing using Claude AI. It monitors GitLab issues with specific labels and automagically creates implementation plans, code changes, and merge requests.

## 🚀 Quick Start

### Installation

Install automagic directly from GitHub using Go:

```bash
go install github.com/bilbo290/automagic@latest
```

### Prerequisites

1. **Go 1.19+** - [Install Go](https://golang.org/doc/install)
2. **Claude CLI** - [Install Claude](https://docs.anthropic.com/en/docs/claude-code)
3. **GitLab Account** with API access
4. **GitLab Personal Access Token** with appropriate permissions

### Required GitLab Token Permissions

Your GitLab Personal Access Token needs the following scopes:
- `api` - Full access to the API
- `read_user` - Read user information
- `read_repository` - Read repository data
- `write_repository` - Write repository data (for creating branches, commits)

## 📋 Configuration

### Environment Variables

automagic uses environment variables for all configuration. You can set them directly in your shell or use a `.env` file.

#### Option 1: Direct Environment Variables

```bash
export GITLAB_URL="https://gitlab.com"
export GITLAB_TOKEN="glpat-your-token-here"
export GITLAB_USERNAME="your-gitlab-username"
export CLAUDE_COMMAND="claude"
export CLAUDE_FLAGS="--dangerously-skip-permissions --output-format stream-json --verbose"
```

#### Option 2: .env File (Recommended)

Generate a template configuration file:

```bash
automagic -generate-config
```

Then edit the generated `.env` file:

```bash
# automagic GitLab Automation Configuration
GITLAB_URL=https://gitlab.com
GITLAB_TOKEN=glpat-your-token-here
GITLAB_USERNAME=your-gitlab-username

CLAUDE_COMMAND=claude
CLAUDE_FLAGS="--dangerously-skip-permissions --output-format stream-json --verbose"

# Optional: Set default project (will be set via interactive mode)
DEFAULT_PROJECT_PATH=

# Optional: Customize daemon behavior
DAEMON_INTERVAL=10
CLAUDE_LABEL=claude
PROCESS_LABEL=picked_up_by_claude
REVIEW_LABEL=waiting_human_review
```

## 🎯 Usage Modes

### Interactive Setup

First, set up your project interactively:

```bash
automagic -interactive
```

This will:
1. List your accessible GitLab projects
2. Let you select a project to monitor
3. Save the selection to `.env` file
4. Optionally process an issue immediately

### Daemon Mode (Recommended)

#### With Memory (SQLite Session Storage)
```bash
automagic --daemon --memory
```

**Features:**
- Persistent session storage
- automagic session resumption when humans comment
- Full conversation history maintained
- Ideal for complex, ongoing issues

#### Without Memory (Fresh Sessions)
```bash
automagic --daemon
```

**Features:**
- Each issue gets a fresh Claude session
- No session persistence
- Claude reads all existing comments for context
- Simpler architecture, easier debugging
- Repository state preserved between sessions

### Single Issue Processing

```bash
# Process a specific issue
automagic -issue 123

# Dry run (see what would happen)
automagic -issue 123 -dry-run

# Semi-dry run (clone repo, show prompt, but don't execute)
automagic -issue 123 -semi-dry-run
```

### Utility Commands

```bash
# List accessible projects
automagic -list-projects

# Search for projects
automagic -search "backend"

# List issues in selected project
automagic -list-issues

# List issues with specific label
automagic -list-issues -label "claude"

# Test label filtering
automagic -test-labels

# Debug GitLab MCP integration
automagic -debug-mcp
```

## 🏷️ Label Workflow

automagic uses a three-label workflow system:

### 1. Starting Work: `claude` Label

```mermaid
graph LR
    A[Issue with 'claude' label] --> B[automagic picks up]
    B --> C[Label: 'picked_up_by_claude']
    C --> D[Claude processes issue]
```

**To start:** Add the `claude` label to any GitLab issue.

### 2. Processing: `picked_up_by_claude` Label

While automagic is processing:
- Issue is labeled `picked_up_by_claude`
- Claude analyzes the issue and existing comments
- Creates implementation plan and posts as comment
- Implements the solution
- Creates merge request
- Updates issue with completion status

### 3. Human Review: `waiting_human_review` Label

```mermaid
graph LR
    A[Claude completes work] --> B[Label: 'waiting_human_review']
    B --> C[Human reviews & comments]
    C --> D[Label: 'picked_up_by_claude']
    D --> E[Claude addresses feedback]
    E --> B
```

After Claude completes:
- Issue is labeled `waiting_human_review`
- Humans review the merge request and implementation
- Add comments with feedback, questions, or requests
- automagic automagically detects human comments and re-engages Claude

### 4. Completion: `solved` Label

When satisfied with the implementation:
- Manually change label to `solved`
- Or remove all workflow labels
- This stops the automation loop

## 🔧 Advanced Configuration

### Custom Claude Flags

Set custom Claude flags in your environment:

```bash
export CLAUDE_FLAGS="--dangerously-skip-permissions --output-format stream-json --verbose --model claude-3-5-sonnet-20241022"
```

### Different Polling Intervals

```bash
export DAEMON_INTERVAL=30  # Check every 30 seconds instead of 10
```

### Custom Label Names

```bash
export CLAUDE_LABEL="ai-help"          # Instead of "claude"
export PROCESS_LABEL="ai-working"      # Instead of "picked_up_by_claude" 
export REVIEW_LABEL="human-review"     # Instead of "waiting_human_review"
```

## 📁 Project Structure

```
your-project/
├── .env               # Configuration file (environment variables)
└── .git/              # Git repository (will be auto-cloned if needed)
```

## 🐛 Troubleshooting

### Common Issues

**1. "GitLab connection test failed"**
```bash
# Check your token and URL
automagic -list-projects
```

**2. "No project configured"**
```bash
# Run interactive setup
automagic -interactive
```

**3. "Claude command not found"**
```bash
# Install Claude CLI
# See: https://docs.anthropic.com/en/docs/claude-code
which claude
```

**4. "Permission denied" errors**
- Check GitLab token permissions
- Ensure token has `api` and `write_repository` scopes

### Debug Mode

Use dry-run modes to debug issues:

```bash
# See exactly what would happen
automagic --daemon -dry-run

# Clone repo and show prompts without executing
automagic --daemon -semi-dry-run
```

### Logs and Monitoring

automagic provides detailed logging:

```bash
# Run daemon with full debug output
automagic --daemon 2>&1 | tee automagic.log
```

## 🔄 Workflow Examples

### Example 1: New Feature Request

1. **Human creates issue**: "Add dark mode toggle to settings page"
2. **Human adds label**: `claude`
3. **automagic picks up**: Changes label to `picked_up_by_claude`
4. **Claude analyzes**: Reads issue description and existing comments
5. **Claude plans**: Posts implementation plan as comment
6. **Claude implements**: Creates branch, writes code, commits changes
7. **Claude delivers**: Creates merge request, updates issue
8. **System updates**: Label changes to `waiting_human_review`
9. **Human reviews**: Checks MR, tests locally, adds feedback comment
10. **automagic re-engages**: Detects human comment, changes label back to `picked_up_by_claude`
11. **Claude iterates**: Addresses feedback, updates implementation
12. **Loop continues**: Until human is satisfied and marks as `solved`

### Example 2: Bug Fix

1. **Issue**: "Login form doesn't validate email addresses properly"
2. **Add label**: `claude`
3. **Claude**: Investigates code, identifies validation logic issue
4. **Claude**: Posts analysis and fix plan as comment
5. **Claude**: Implements fix, adds tests, creates MR
6. **Human**: Reviews, requests additional test cases
7. **Claude**: Adds more comprehensive tests
8. **Human**: Approves and merges, marks `solved`

## 🚀 Best Practices

### Issue Description Quality

Write clear, detailed issue descriptions:

```markdown
## Problem
The user login form accepts invalid email addresses like "user@" or "user.com"

## Expected Behavior
Only valid email addresses should be accepted (user@domain.com format)

## Acceptance Criteria
- [ ] Email validation follows RFC 5322 standard
- [ ] Clear error messages for invalid emails
- [ ] Unit tests cover edge cases
- [ ] Integration tests verify form behavior
```

### Effective Human Feedback

When reviewing Claude's work:

```markdown
## Code Review Feedback

✅ **Good:**
- Implementation logic is correct
- Tests cover main scenarios

🔄 **Needs Changes:**
- Please add validation for edge case: empty domain
- Error message should be more user-friendly
- Add test for maximum email length (320 chars)

## Questions
- Should we support internationalized domain names?
- What about plus addressing (user+tag@domain.com)?
```

### Repository Management

- Keep your repositories clean and up-to-date
- Use meaningful branch names (automagic creates `issue-{number}` branches)
- Review and merge automagic's MRs promptly to avoid conflicts

## 🔒 Security Considerations

- Store GitLab tokens securely (use environment variables in production)
- Review all code changes before merging
- Use appropriate GitLab project permissions
- Consider running automagic in a isolated environment for production use

## 📚 Additional Resources

- [Claude Code Documentation](https://docs.anthropic.com/en/docs/claude-code)
- [GitLab API Documentation](https://docs.gitlab.com/ee/api/)
- [automagic GitHub Repository](https://github.com/your-username/automagic)

## 🤝 Contributing

Contributions are welcome! Please see [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines.

## 📄 License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.