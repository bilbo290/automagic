# Testing Merge Request Functionality

## New Features Added

### 1. GitLab Client Extensions
- Added `MergeRequest` struct with comprehensive fields
- Added `GetAssignedMergeRequests()` method to fetch MRs assigned to a user
- Added `GetMergeRequestsForReview()` method to fetch MRs where user is reviewer
- Added `GetProjectMergeRequests()` method to fetch MRs from a specific project
- Added `GetMergeRequest()` method to fetch a specific MR
- Added `GetMergeRequestDiscussions()` method to fetch MR discussions
- Added `CreateMergeRequestNote()` method to add comments to MRs

### 2. Daemon Integration
- Added `checkForMergeRequestsWithContext()` to check for assigned MRs in daemon mode
- Added `processMergeRequestWithClaude()` to process MRs with Claude
- Integrated MR checking into both `Run()` and `RunWithoutMemory()` daemon loops
- Added tracking for processed MRs to avoid duplicates

### 3. Command Line Interface
- Added `-list-mrs` flag to list assigned merge requests
- Added `-review-mr <id>` flag to review specific merge request with Claude
- Updated help text to include new MR commands

## Testing Commands

### List assigned merge requests:
```bash
./automagic -list-mrs
```

### Review a specific merge request:
```bash
./automagic -review-mr 123
```

### Run daemon with MR support:
```bash
./automagic -daemon
```

## How It Works

1. **Daemon Mode**: The daemon now checks for:
   - Issues with 'claude' label (existing functionality)
   - Assigned merge requests for the configured user
   - Issues under review with new human comments

2. **MR Processing**: When a merge request is found:
   - Claude is given a specialized prompt for code review
   - The prompt includes MR details and review instructions
   - Claude uses GitLab MCP tools to fetch MR changes and provide feedback

3. **Review Criteria**: Claude reviews code for:
   - Code quality and best practices
   - Security vulnerabilities
   - Performance issues
   - Documentation completeness
   - Test coverage

## Configuration

The functionality uses the existing GitLab configuration:
- `GITLAB_URL` - GitLab instance URL
- `GITLAB_TOKEN` - Personal access token
- `GITLAB_USERNAME` - Username for checking assigned MRs

## Next Steps

The functionality is ready for testing. The daemon will now:
1. Check for new issues with 'claude' label
2. Check for assigned merge requests
3. Check for issues under review with new comments
4. Process all types with appropriate Claude prompts