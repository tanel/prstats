package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

func configDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".prstats"
	}

	return filepath.Join(home, ".prstats")
}

func configFile(name string) string {
	return filepath.Join(configDir(), name)
}

var usersFile = configFile("users.json")
var settingsFile = configFile("settings.json")

type Settings struct {
	ApprovalsRequired int    `json:"approvals_required"`
	Team              string `json:"team"`
	Repo              string `json:"repo"`
}

type PullRequest struct {
	Author  string
	Draft   bool
	Reviews []Review
}

type Review struct {
	Author string
	State  string
}

type GitHubUser struct {
	Login string `json:"login"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

type CachedUser struct {
	Name    string `json:"name"`
	Email   string `json:"email"`
	Login   string `json:"login"`
	Enabled bool   `json:"enabled"`
	Team    string `json:"team"`
}

func main() {
	sinceFlag := flag.String("since", "", "time period (e.g. '1 week', '2 weeks', '30 days', '3 months'); defaults to this week")
	teamFlag := flag.String("team", "", "filter by team name")
	repoFlag := flag.String("repo", "", "repository in owner/repo format (required)")
	obfuscateFlag := flag.Bool("obfuscate", false, "replace names with User1, User2, etc.")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: prstats [flags]\n\nFlags:\n")
		flag.PrintDefaults()
	}

	flag.Parse()

	if err := os.MkdirAll(configDir(), 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating config directory: %v\n", err)
		os.Exit(1)
	}

	settings, err := loadSettings()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading settings: %v\n", err)
		os.Exit(1)
	}

	if *teamFlag == "" && settings.Team != "" {
		*teamFlag = settings.Team
	}

	if *repoFlag == "" && settings.Repo != "" {
		*repoFlag = settings.Repo
	}

	if *repoFlag == "" {
		fmt.Fprintf(os.Stderr, "-repo flag is required (or set 'repo' in ~/.prstats/settings.json)\n")
		os.Exit(1)
	}

	repo := *repoFlag
	parts := strings.SplitN(repo, "/", 2)
	if len(parts) != 2 {
		fmt.Fprintf(os.Stderr, "Repository must be in owner/repo format\n")
		os.Exit(1)
	}

	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		fmt.Fprintf(os.Stderr, "GITHUB_TOKEN environment variable is required\n")
		os.Exit(1)
	}

	var since time.Time
	if *sinceFlag == "" {
		now := time.Now()
		weekday := int(now.Weekday())
		if weekday == 0 {
			weekday = 7
		}
		since = now.AddDate(0, 0, -(weekday - 1))
		since = time.Date(since.Year(), since.Month(), since.Day(), 0, 0, 0, 0, since.Location())
	} else {
		var err error
		since, err = parseSince(*sinceFlag)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Invalid --since value: %v\n", err)
			os.Exit(1)
		}
	}

	prs, err := fetchPRs(parts[0], parts[1], token, since)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error fetching PRs: %v\n", err)
		os.Exit(1)
	}

	cache, err := loadUsers()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading users cache: %v\n", err)
		os.Exit(1)
	}

	teamLogins := map[string]bool{}
	for login, user := range cache {
		if !user.Enabled {
			continue
		}
		if *teamFlag != "" && !strings.EqualFold(user.Team, *teamFlag) {
			continue
		}
		teamLogins[login] = true
	}

	pendingResult, err := fetchPendingReviews(parts[0], parts[1], token, teamLogins)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error fetching pending reviews: %v\n", err)
		os.Exit(1)
	}

	pendingReviews := pendingResult.pending
	openPRs := pendingResult.openPRs
	openPRURLs := pendingResult.openPRURLs

	created := map[string]int{}
	approvals := map[string]int{}
	for _, pr := range prs {
		if pr.Draft {
			continue
		}

		created[pr.Author]++

		seen := map[string]bool{}
		for _, review := range pr.Reviews {
			if review.State != "APPROVED" {
				continue
			}

			if review.Author == pr.Author {
				continue
			}

			if seen[review.Author] {
				continue
			}

			seen[review.Author] = true
			approvals[review.Author]++
		}
	}

	allLogins := map[string]bool{}
	for login := range created {
		allLogins[login] = true
	}

	for login := range approvals {
		allLogins[login] = true
	}

	for login := range pendingReviews {
		allLogins[login] = true
	}

	for login := range openPRs {
		allLogins[login] = true
	}

	for login, user := range cache {
		if !user.Enabled {
			continue
		}

		if *teamFlag != "" && !strings.EqualFold(user.Team, *teamFlag) {
			continue
		}

		allLogins[login] = true
	}

	type userCount struct {
		display        string
		login          string
		created        int
		approvals      int
		openPRs        int
		pendingReviews int
	}

	var sorted []userCount
	for login := range allLogins {
		display := displayName(login, token, cache)
		if cached, ok := cache[login]; ok && !cached.Enabled {
			continue
		}

		if *teamFlag != "" {
			if cached, ok := cache[login]; !ok || !strings.EqualFold(cached.Team, *teamFlag) {
				continue
			}
		}

		sorted = append(sorted, userCount{
			display:        display,
			login:          login,
			created:        created[login],
			approvals:      approvals[login],
			openPRs:        openPRs[login],
			pendingReviews: pendingReviews[login],
		})
	}

	if err := saveUsers(cache); err != nil {
		fmt.Fprintf(os.Stderr, "Error saving users cache: %v\n", err)
		os.Exit(1)
	}

	sort.Slice(sorted, func(i, j int) bool {
		return strings.ToLower(sorted[i].display) < strings.ToLower(sorted[j].display)
	})

	if *obfuscateFlag {
		for i := range sorted {
			sorted[i].display = fmt.Sprintf("User%d", i+1)
		}
	}

	now := time.Now()
	if *teamFlag != "" {
		fmt.Printf("PRs for %s, team %s (%s - %s):\n\n", repo, *teamFlag, since.Format("2006-01-02"), now.Format("2006-01-02"))
	} else {
		fmt.Printf("PRs for %s (%s - %s):\n\n", repo, since.Format("2006-01-02"), now.Format("2006-01-02"))
	}
	reviewerToPRs := map[string][]openPR{}
	var allPendingPRs []openPR
	for _, uc := range sorted {
		for _, pr := range openPRURLs[uc.login] {
			allPendingPRs = append(allPendingPRs, pr)
			for _, login := range pr.PendingReviewers {
				reviewerToPRs[login] = append(reviewerToPRs[login], pr)
			}
		}
	}

	for i := range sorted {
		sorted[i].pendingReviews = len(reviewerToPRs[sorted[i].login])
	}

	fmt.Printf("%-30s %-15s %-15s %-20s %-15s\n", "User", "PRs Created", "PRs Approved", "Reviews Waiting", "Review Load")
	fmt.Printf("%-30s %-15s %-15s %-20s %-15s\n", "----", "-----------", "------------", "---------------", "-----------")

	totalCreated := 0
	totalApprovals := 0
	totalWaiting := 0
	for _, uc := range sorted {
		reviewLoad := uc.approvals + uc.pendingReviews
		fmt.Printf("%-30s %-15d %-15d %-20d %-15d\n", uc.display, uc.created, uc.approvals, uc.pendingReviews, reviewLoad)
		totalCreated += uc.created
		totalApprovals += uc.approvals
		totalWaiting += uc.pendingReviews
	}

	fmt.Printf("%-30s %-15s %-15s %-20s %-15s\n", "----", "-----------", "------------", "---------------", "-----------")
	fmt.Printf("%-30s %-15d %-15d %-20d\n", "Total", totalCreated, totalApprovals, totalWaiting)

	type reviewerPRs struct {
		name string
		prs  []openPR
	}

	var reviewerList []reviewerPRs
	for login, prs := range reviewerToPRs {
		reviewerList = append(reviewerList, reviewerPRs{
			name: displayName(login, token, cache),
			prs:  prs,
		})
	}

	sort.Slice(reviewerList, func(i, j int) bool {
		return strings.ToLower(reviewerList[i].name) < strings.ToLower(reviewerList[j].name)
	})

	sort.Slice(allPendingPRs, func(i, j int) bool {
		return allPendingPRs[i].CreatedAt.Before(allPendingPRs[j].CreatedAt)
	})

	fmt.Printf("\nPRs waiting for review: %d\n", len(allPendingPRs))
	for _, pr := range allPendingPRs {
		if len(pr.PendingReviewers) == 0 {
			continue
		}
		var reviewerNames []string
		for _, login := range pr.PendingReviewers {
			reviewerNames = append(reviewerNames, displayName(login, token, cache))
		}
		sort.Strings(reviewerNames)
		fmt.Printf("  %s (%s) - waiting on: %s\n", pr.URL, formatAge(pr.CreatedAt), strings.Join(reviewerNames, ", "))
	}

	type ownerCount struct {
		name  string
		count int
		prs   []openPR
	}

	var blocked []ownerCount
	for _, uc := range sorted {
		prs := openPRURLs[uc.login]
		if len(prs) > 0 {
			blocked = append(blocked, ownerCount{uc.display, len(prs), prs})
		}
	}

	sort.Slice(blocked, func(i, j int) bool {
		return strings.ToLower(blocked[i].name) < strings.ToLower(blocked[j].name)
	})

	fmt.Printf("\nBlocked by missing reviews: %d developers\n", len(blocked))
	for _, b := range blocked {
		fmt.Printf("  %s (%d PRs)\n", b.name, b.count)
		for _, pr := range b.prs {
			fmt.Printf("    %s (%s)\n", pr.URL, formatAge(pr.CreatedAt))
		}
	}

	var tooManyReviewers []openPR
	for _, pr := range allPendingPRs {
		if len(pr.PendingReviewers) > 2 {
			tooManyReviewers = append(tooManyReviewers, pr)
		}
	}
	if len(tooManyReviewers) > 0 {
		fmt.Printf("\nPRs with too many reviewers assigned (do not request from more than %d devs):\n", settings.ApprovalsRequired)
		for _, pr := range tooManyReviewers {
			var reviewerNames []string
			for _, login := range pr.PendingReviewers {
				reviewerNames = append(reviewerNames, displayName(login, token, cache))
			}
			sort.Strings(reviewerNames)
			fmt.Printf("  %s (%s) - waiting on: %s\n", pr.URL, formatAge(pr.CreatedAt), strings.Join(reviewerNames, ", "))
		}
	}

	fmt.Printf("\nPending review assignments:\n")
	for _, r := range reviewerList {
		fmt.Printf("  %s (%d PRs)\n", r.name, len(r.prs))
		for _, pr := range r.prs {
			fmt.Printf("    %s (%s)\n", pr.URL, formatAge(pr.CreatedAt))
		}
	}

	reviewCandidates := make([]userCount, len(sorted))
	copy(reviewCandidates, sorted)
	sort.Slice(reviewCandidates, func(i, j int) bool {
		si := reviewCandidates[i].approvals + reviewCandidates[i].pendingReviews
		sj := reviewCandidates[j].approvals + reviewCandidates[j].pendingReviews
		return si < sj
	})

	fmt.Printf("\nWho to request review from (pick devs with less load):\n")
	for _, uc := range reviewCandidates {
		fmt.Printf("  %s (review load: %d)\n", uc.display, uc.approvals+uc.pendingReviews)
	}
}

func formatAge(t time.Time) string {
	d := time.Since(t)
	days := int(d.Hours() / 24)
	if days == 0 {
		hours := int(d.Hours())
		if hours == 0 {
			return "just now"
		}
		return fmt.Sprintf("%dh", hours)
	}
	if days < 7 {
		return fmt.Sprintf("%dd", days)
	}
	weeks := days / 7
	remainder := days % 7
	if remainder == 0 {
		return fmt.Sprintf("%dw", weeks)
	}
	return fmt.Sprintf("%dw %dd", weeks, remainder)
}

func loadUsers() (map[string]*CachedUser, error) {
	cache := map[string]*CachedUser{}

	data, err := os.ReadFile(usersFile)
	if err != nil {
		if os.IsNotExist(err) {
			return cache, nil
		}

		return nil, err
	}

	var users []*CachedUser
	if err := json.Unmarshal(data, &users); err != nil {
		return nil, err
	}

	for _, user := range users {
		cache[user.Login] = user
	}

	return cache, nil
}

func saveUsers(cache map[string]*CachedUser) error {
	var users []*CachedUser
	for _, user := range cache {
		users = append(users, user)
	}

	data, err := json.MarshalIndent(users, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(usersFile, data, 0644)
}

func parseSince(s string) (time.Time, error) {
	parts := strings.Fields(s)
	if len(parts) != 2 {
		return time.Time{}, fmt.Errorf("expected format: '<number> <unit>' (e.g. '1 week')")
	}

	n, err := strconv.Atoi(parts[0])
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid number: %s", parts[0])
	}

	now := time.Now()
	switch strings.TrimSuffix(parts[1], "s") {
	case "day":
		return now.AddDate(0, 0, -n), nil
	case "week":
		return now.AddDate(0, 0, -n*7), nil
	case "month":
		return now.AddDate(0, -n, 0), nil
	case "year":
		return now.AddDate(-n, 0, 0), nil
	default:
		return time.Time{}, fmt.Errorf("unknown unit: %s (use day, week, month, year)", parts[1])
	}
}

func loadSettings() (*Settings, error) {
	data, err := os.ReadFile(settingsFile)
	if err != nil {
		if os.IsNotExist(err) {
			defaults := Settings{ApprovalsRequired: 1}
			b, err := json.MarshalIndent(defaults, "", "  ")
			if err != nil {
				return nil, err
			}

			if err := os.WriteFile(settingsFile, b, 0644); err != nil {
				return nil, err
			}

			fmt.Fprintf(os.Stderr, "Created %s with default values. Please edit it before running again.\n", settingsFile)
			os.Exit(0)
		}

		return nil, err
	}

	var settings Settings
	if err := json.Unmarshal(data, &settings); err != nil {
		return nil, err
	}

	return &settings, nil
}

func displayName(login, token string, cache map[string]*CachedUser) string {
	user, ok := cache[login]
	if !ok {
		ghUser, err := fetchUser(login, token)
		if err != nil {
			return login
		}

		user = &CachedUser{
			Name:    ghUser.Name,
			Email:   ghUser.Email,
			Login:   ghUser.Login,
			Enabled: true,
		}

		cache[login] = user
	}

	if user.Name != "" {
		return user.Name
	}

	return login
}

func fetchUser(login, token string) (*GitHubUser, error) {
	u := fmt.Sprintf("https://api.github.com/users/%s", login)

	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}

	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned status %d", resp.StatusCode)
	}

	var user GitHubUser
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		return nil, err
	}

	return &user, nil
}

type graphQLRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables"`
}

type graphQLResponse struct {
	Data struct {
		Repository struct {
			PullRequests struct {
				PageInfo struct {
					HasNextPage bool   `json:"hasNextPage"`
					EndCursor   string `json:"endCursor"`
				} `json:"pageInfo"`
				Nodes []struct {
					Author struct {
						Login string `json:"login"`
					} `json:"author"`
					CreatedAt time.Time `json:"createdAt"`
					IsDraft   bool      `json:"isDraft"`
					Labels    struct {
						Nodes []struct {
							Name string `json:"name"`
						} `json:"nodes"`
					} `json:"labels"`
					ReviewThreads struct {
						Nodes []struct {
							IsResolved bool `json:"isResolved"`
						} `json:"nodes"`
					} `json:"reviewThreads"`
					Reviews struct {
						Nodes []struct {
							Author struct {
								Login string `json:"login"`
							} `json:"author"`
							State string `json:"state"`
						} `json:"nodes"`
					} `json:"reviews"`
				} `json:"nodes"`
			} `json:"pullRequests"`
		} `json:"repository"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

const prQuery = `
query($owner: String!, $name: String!, $after: String) {
  repository(owner: $owner, name: $name) {
    pullRequests(first: 100, orderBy: {field: CREATED_AT, direction: DESC}, after: $after) {
      pageInfo {
        hasNextPage
        endCursor
      }
      nodes {
        author { login }
        createdAt
        isDraft
        labels(first: 100) {
          nodes { name }
        }
        reviewThreads(first: 100) {
          nodes { isResolved }
        }
        reviews(first: 100) {
          nodes {
            author { login }
            state
          }
        }
      }
    }
  }
}
`

const pendingReviewQuery = `
query($owner: String!, $name: String!, $after: String) {
  repository(owner: $owner, name: $name) {
    pullRequests(first: 100, states: [OPEN], after: $after) {
      pageInfo {
        hasNextPage
        endCursor
      }
      nodes {
        isDraft
        url
        createdAt
        author { login }
        labels(first: 100) {
          nodes { name }
        }
        reviewThreads(first: 100) {
          nodes { isResolved }
        }
        reviewRequests(first: 100) {
          nodes {
            requestedReviewer {
              ... on User { login }
            }
          }
        }
        reviews(first: 100) {
          nodes {
            author { login }
            state
          }
        }
        timelineItems(first: 100, itemTypes: [LABELED_EVENT, REVIEW_REQUESTED_EVENT, CONVERT_TO_DRAFT_EVENT, READY_FOR_REVIEW_EVENT]) {
          nodes {
            ... on LabeledEvent {
              createdAt
              label { name }
            }
            ... on ReviewRequestedEvent {
              createdAt
              requestedReviewer {
                ... on User { login }
              }
            }
            ... on ConvertToDraftEvent {
              __typename
              createdAt
            }
            ... on ReadyForReviewEvent {
              __typename
              createdAt
            }
          }
        }
      }
    }
  }
}
`

type pendingReviewResponse struct {
	Data struct {
		Repository struct {
			PullRequests struct {
				PageInfo struct {
					HasNextPage bool   `json:"hasNextPage"`
					EndCursor   string `json:"endCursor"`
				} `json:"pageInfo"`
				Nodes []struct {
					IsDraft   bool      `json:"isDraft"`
					URL       string    `json:"url"`
					CreatedAt time.Time `json:"createdAt"`
					Author    struct {
						Login string `json:"login"`
					} `json:"author"`
					Labels struct {
						Nodes []struct {
							Name string `json:"name"`
						} `json:"nodes"`
					} `json:"labels"`
					ReviewThreads struct {
						Nodes []struct {
							IsResolved bool `json:"isResolved"`
						} `json:"nodes"`
					} `json:"reviewThreads"`
					ReviewRequests struct {
						Nodes []struct {
							RequestedReviewer struct {
								Login string `json:"login"`
							} `json:"requestedReviewer"`
						} `json:"nodes"`
					} `json:"reviewRequests"`
					Reviews struct {
						Nodes []struct {
							Author struct {
								Login string `json:"login"`
							} `json:"author"`
							State string `json:"state"`
						} `json:"nodes"`
					} `json:"reviews"`
					TimelineItems struct {
						Nodes []struct {
							Typename  string    `json:"__typename"`
							CreatedAt time.Time `json:"createdAt"`
							Label     struct {
								Name string `json:"name"`
							} `json:"label"`
							RequestedReviewer struct {
								Login string `json:"login"`
							} `json:"requestedReviewer"`
						} `json:"nodes"`
					} `json:"timelineItems"`
				} `json:"nodes"`
			} `json:"pullRequests"`
		} `json:"repository"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

type openPR struct {
	URL              string
	CreatedAt        time.Time
	PendingReviewers []string
	Approvers        []string
}

type pendingReviewsResult struct {
	pending    map[string]int
	openPRs    map[string]int
	openPRURLs map[string][]openPR
}

func fetchPendingReviews(owner, name, token string, teamLogins map[string]bool) (*pendingReviewsResult, error) {
	result := &pendingReviewsResult{
		pending:    map[string]int{},
		openPRs:    map[string]int{},
		openPRURLs: map[string][]openPR{},
	}
	var cursor *string

	for {
		variables := map[string]any{
			"owner": owner,
			"name":  name,
		}

		if cursor != nil {
			variables["after"] = *cursor
		}

		body, err := json.Marshal(graphQLRequest{
			Query:     pendingReviewQuery,
			Variables: variables,
		})

		if err != nil {
			return nil, err
		}

		req, err := http.NewRequest("POST", "https://api.github.com/graphql", bytes.NewReader(body))
		if err != nil {
			return nil, err
		}

		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, err
		}

		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("GitHub GraphQL API returned status %d", resp.StatusCode)
		}

		var gqlResult pendingReviewResponse
		if err := json.NewDecoder(resp.Body).Decode(&gqlResult); err != nil {
			return nil, err
		}

		if len(gqlResult.Errors) > 0 {
			return nil, fmt.Errorf("GraphQL error: %s", gqlResult.Errors[0].Message)
		}

		nodes := gqlResult.Data.Repository.PullRequests.Nodes
		if len(nodes) == 0 {
			break
		}

		for _, node := range nodes {
			if node.IsDraft {
				continue
			}

			if len(node.Labels.Nodes) == 0 {
				continue
			}

			skipPR := false
			for _, l := range node.Labels.Nodes {
				if strings.EqualFold(l.Name, "don't merge") {
					skipPR = true
					break
				}
			}

			if skipPR {
				continue
			}

			for _, t := range node.ReviewThreads.Nodes {
				if !t.IsResolved {
					skipPR = true
					break
				}
			}

			if skipPR {
				continue
			}

			result.openPRs[node.Author.Login]++

			hasReadyForMerge := false
			hasChangesRequired := false
			for _, l := range node.Labels.Nodes {
				if strings.EqualFold(l.Name, "ready for merge") {
					hasReadyForMerge = true
				}

				if strings.EqualFold(l.Name, "changes required") {
					hasChangesRequired = true
				}
			}

			approved := map[string]bool{}
			for _, r := range node.Reviews.Nodes {
				if r.State == "APPROVED" {
					approved[r.Author.Login] = true
				}
			}

			var pendingReviewers []string
			for _, rr := range node.ReviewRequests.Nodes {
				login := rr.RequestedReviewer.Login
				if login == "" {
					continue
				}

				if !approved[login] {
					result.pending[login]++
					pendingReviewers = append(pendingReviewers, login)
				}
			}

			if !hasReadyForMerge && !hasChangesRequired {
				// Track draft periods to find when PR was out of draft.
				// PR starts as non-draft (isDraft PRs are skipped above).
				type draftToggle struct {
					at      time.Time
					isDraft bool
				}
				toggles := []draftToggle{{node.CreatedAt, false}}
				for _, item := range node.TimelineItems.Nodes {
					switch item.Typename {
					case "ConvertToDraftEvent":
						toggles = append(toggles, draftToggle{item.CreatedAt, true})
					case "ReadyForReviewEvent":
						toggles = append(toggles, draftToggle{item.CreatedAt, false})
					}
				}

				isDraftAt := func(t time.Time) bool {
					draft := false
					for _, tg := range toggles {
						if !tg.at.After(t) {
							draft = tg.isDraft
						}
					}
					return draft
				}

				var readyAt time.Time
				for _, item := range node.TimelineItems.Nodes {
					if item.RequestedReviewer.Login == "" {
						continue
					}
					if !teamLogins[item.RequestedReviewer.Login] {
						continue
					}
					if isDraftAt(item.CreatedAt) {
						continue
					}
					if readyAt.IsZero() || item.CreatedAt.After(readyAt) {
						readyAt = item.CreatedAt
					}
				}
				if readyAt.IsZero() {
					readyAt = node.CreatedAt
				}

				var approvers []string
				for login := range approved {
					approvers = append(approvers, login)
				}
				sort.Strings(approvers)
				result.openPRURLs[node.Author.Login] = append(result.openPRURLs[node.Author.Login], openPR{URL: node.URL, CreatedAt: readyAt, PendingReviewers: pendingReviewers, Approvers: approvers})
			}
		}

		if !gqlResult.Data.Repository.PullRequests.PageInfo.HasNextPage {
			break
		}

		cursor = &gqlResult.Data.Repository.PullRequests.PageInfo.EndCursor
	}

	return result, nil
}

func fetchPRs(owner, name, token string, since time.Time) ([]PullRequest, error) {
	var allPRs []PullRequest
	var cursor *string

	for {
		variables := map[string]any{
			"owner": owner,
			"name":  name,
		}

		if cursor != nil {
			variables["after"] = *cursor
		}

		body, err := json.Marshal(graphQLRequest{
			Query:     prQuery,
			Variables: variables,
		})

		if err != nil {
			return nil, err
		}

		req, err := http.NewRequest("POST", "https://api.github.com/graphql", bytes.NewReader(body))
		if err != nil {
			return nil, err
		}

		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, err
		}

		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("GitHub GraphQL API returned status %d", resp.StatusCode)
		}

		var result graphQLResponse
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			return nil, err
		}

		if len(result.Errors) > 0 {
			return nil, fmt.Errorf("GraphQL error: %s", result.Errors[0].Message)
		}

		nodes := result.Data.Repository.PullRequests.Nodes
		if len(nodes) == 0 {
			break
		}

		done := false
		for _, node := range nodes {
			if node.CreatedAt.Before(since) {
				done = true
				break
			}

			if len(node.Labels.Nodes) == 0 {
				continue
			}

			skipPR := false
			for _, l := range node.Labels.Nodes {
				if strings.EqualFold(l.Name, "don't merge") {
					skipPR = true
					break
				}
			}

			if skipPR {
				continue
			}

			for _, t := range node.ReviewThreads.Nodes {
				if !t.IsResolved {
					skipPR = true
					break
				}
			}

			if skipPR {
				continue
			}

			var reviews []Review
			for _, r := range node.Reviews.Nodes {
				reviews = append(reviews, Review{
					Author: r.Author.Login,
					State:  r.State,
				})
			}

			allPRs = append(allPRs, PullRequest{
				Author:  node.Author.Login,
				Draft:   node.IsDraft,
				Reviews: reviews,
			})
		}

		if done || !result.Data.Repository.PullRequests.PageInfo.HasNextPage {
			break
		}

		cursor = &result.Data.Repository.PullRequests.PageInfo.EndCursor
	}

	return allPRs, nil
}
