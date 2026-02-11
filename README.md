# prstats

CLI tool that fetches GitHub pull request statistics for a team.

## What it shows

- Per-developer table: PRs created, PRs approved, reviews waiting, and review load
- Blocked by missing reviews: developers whose PRs are waiting on reviewers
- PRs waiting for review: open PRs with pending reviewer assignments
- PRs with too many reviewers assigned
- Pending review assignments: which developer owes which reviews
- Who to request review from: developers sorted by lowest review load (start from the top)

## Installation

```
go install github.com/tanel/prstats@latest
```

Or build from source:

```
git clone https://github.com/tanel/prstats
cd prstats
go build -o prstats .
```

## Requirements

A GitHub personal access token with `repo` scope, set as. You can create one at https://github.com/settings/tokens.

```
export GITHUB_TOKEN=your_token
```

## Usage

```
prstats -repo owner/repo
prstats -repo owner/repo -team backend
prstats -repo owner/repo -since "2 weeks"
```

## Flags

| Flag | Description |
|------|-------------|
| `-repo` | Repository in `owner/repo` format (required, or set in `settings.json`) |
| `-team` | Filter by team name (or set in `settings.json`) |
| `-since` | Time period, e.g. `1 week`, `2 weeks`, `30 days`, `3 months`. Defaults to the current week. |
| `-obfuscate` | Replace developer names with User1, User2, etc. |

## Configuration

On first run, `~/.prstats/settings.json` is created automatically. Edit it before running again:

```json
{
  "approvals_required": 2,
  "team": "backend",
  "repo": "owner/repo"
}
```

| Field | Description |
|-------|-------------|
| `approvals_required` | Number of approvals required per PR. Used to detect over-assigned reviewers. |
| `team` | Default team filter. Can be overridden with `-team`. |
| `repo` | Default repository. Can be overridden with `-repo`. |

### users.json

`~/.prstats/users.json` is auto-populated with GitHub user data on each run. You can set `"enabled": false` on a user to exclude them from all output.

Each user also has a `"team"` field you can fill in to enable team filtering:

```json
[
  { "login": "alice", "name": "Alice", "team": "backend", "enabled": true },
  { "login": "bob",   "name": "Bob",   "team": "frontend", "enabled": true }
]
```
