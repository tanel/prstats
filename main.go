package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"time"
)

type PullRequest struct {
	User struct {
		Login string `json:"login"`
	} `json:"user"`
	CreatedAt time.Time `json:"created_at"`
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: prstats <owner/repo>\n")
		os.Exit(1)
	}

	repo := os.Args[1]
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		fmt.Fprintf(os.Stderr, "GITHUB_TOKEN environment variable is required\n")
		os.Exit(1)
	}

	since := time.Now().AddDate(0, 0, -7)

	prs, err := fetchPRs(repo, token, since)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error fetching PRs: %v\n", err)
		os.Exit(1)
	}

	counts := map[string]int{}
	for _, pr := range prs {
		counts[pr.User.Login]++
	}

	type userCount struct {
		user  string
		count int
	}

	var sorted []userCount
	for user, count := range counts {
		sorted = append(sorted, userCount{user, count})
	}

	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].count > sorted[j].count
	})

	now := time.Now()
	fmt.Printf("PRs created for %s (%s - %s):\n\n", repo, since.Format("2006-01-02"), now.Format("2006-01-02"))
	fmt.Printf("%-30s %s\n", "User", "PRs Created")
	fmt.Printf("%-30s %s\n", "----", "-----------")
	for _, uc := range sorted {
		fmt.Printf("%-30s %d\n", uc.user, uc.count)
	}

	fmt.Printf("\nTotal: %d PRs\n", len(prs))
}

func fetchPRs(repo, token string, since time.Time) ([]PullRequest, error) {
	var allPRs []PullRequest
	page := 1

	for {
		u := fmt.Sprintf("https://api.github.com/repos/%s/pulls?state=all&sort=created&direction=desc&per_page=100&page=%d", repo, page)

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

		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("GitHub API returned status %d", resp.StatusCode)
		}

		var prs []PullRequest
		if err := json.NewDecoder(resp.Body).Decode(&prs); err != nil {
			return nil, err
		}

		if len(prs) == 0 {
			break
		}

		for _, pr := range prs {
			if pr.CreatedAt.Before(since) {
				return allPRs, nil
			}
			allPRs = append(allPRs, pr)
		}

		page++
	}

	return allPRs, nil
}
