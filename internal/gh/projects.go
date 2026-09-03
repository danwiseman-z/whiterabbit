package gh

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// WorkItem is an issue or pull request assigned to the user that sits on a
// GitHub Project board, together with the status column it is in.
type WorkItem struct {
	Repo      string
	Number    int
	Title     string
	URL       string
	IsPR      bool
	IsDraft   bool
	UpdatedAt time.Time
	// Project is the title of the GitHub Project the item belongs to.
	Project string
	// Status is the item's value in the project's Status field.
	Status string
}

// Ref is the item's reference for listings: "#12" for an issue, "PR #12" for
// a pull request.
func (w WorkItem) Ref() string {
	if w.IsPR {
		return fmt.Sprintf("PR #%d", w.Number)
	}
	return fmt.Sprintf("#%d", w.Number)
}

// RepoName is the repo without its owner prefix.
func (w WorkItem) RepoName() string {
	if i := strings.Index(w.Repo, "/"); i >= 0 {
		return w.Repo[i+1:]
	}
	return w.Repo
}

// ErrProjectScope is returned when the gh token cannot read Projects.
var ErrProjectScope = fmt.Errorf("the gh token cannot read projects: run `gh auth refresh -s read:project`")

// maxAssignedPages bounds how far the assigned-items search is paged.
const maxAssignedPages = 4

// assignedQuery finds open issues and pull requests assigned to the caller,
// with the project boards each one sits on and its status there. Searching
// from the user's side rather than walking every board keeps this to a
// handful of requests regardless of how big the boards are.
const assignedQuery = `
query($q: String!, $after: String) {
  search(query: $q, type: ISSUE, first: 50, after: $after) {
    pageInfo { hasNextPage endCursor }
    nodes {
      __typename
      ... on Issue {
        number title url updatedAt
        repository { nameWithOwner }
        projectItems(first: 20, includeArchived: false) {
          nodes {
            project { title }
            fieldValueByName(name: "Status") {
              ... on ProjectV2ItemFieldSingleSelectValue { name }
            }
          }
        }
      }
      ... on PullRequest {
        number title url updatedAt isDraft
        repository { nameWithOwner }
        projectItems(first: 20, includeArchived: false) {
          nodes {
            project { title }
            fieldValueByName(name: "Status") {
              ... on ProjectV2ItemFieldSingleSelectValue { name }
            }
          }
        }
      }
    }
  }
}`

type assignedPage struct {
	Data struct {
		Search struct {
			PageInfo struct {
				HasNextPage bool   `json:"hasNextPage"`
				EndCursor   string `json:"endCursor"`
			} `json:"pageInfo"`
			Nodes []struct {
				Typename   string    `json:"__typename"`
				Number     int       `json:"number"`
				Title      string    `json:"title"`
				URL        string    `json:"url"`
				UpdatedAt  time.Time `json:"updatedAt"`
				IsDraft    bool      `json:"isDraft"`
				Repository struct {
					NameWithOwner string `json:"nameWithOwner"`
				} `json:"repository"`
				ProjectItems struct {
					Nodes []struct {
						Project struct {
							Title string `json:"title"`
						} `json:"project"`
						FieldValueByName *struct {
							Name string `json:"name"`
						} `json:"fieldValueByName"`
					} `json:"nodes"`
				} `json:"projectItems"`
			} `json:"nodes"`
		} `json:"search"`
	} `json:"data"`
}

// graphql runs a GraphQL query through gh with string variables.
func (c *Client) graphql(ctx context.Context, query string, vars map[string]string, out any) error {
	args := []string{"api", "graphql", "-f", "query=" + query}
	keys := make([]string, 0, len(vars))
	for k := range vars {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		args = append(args, "-f", k+"="+vars[k])
	}
	data, err := c.output(ctx, args...)
	if err != nil {
		if strings.Contains(err.Error(), "read:project") {
			return ErrProjectScope
		}
		return fmt.Errorf("gh api graphql: %w", err)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decoding graphql response: %w", err)
	}
	return nil
}

// InProgress returns every open issue or pull request assigned to the
// authenticated user whose project Status matches one of statuses
// (case-insensitively). An item on several boards appears once per board.
// Draft issues that exist only on a board are not searchable, so they are
// not included.
func (c *Client) InProgress(ctx context.Context, statuses []string) ([]WorkItem, error) {
	if len(statuses) == 0 {
		return nil, nil
	}
	var items []WorkItem
	vars := map[string]string{"q": "is:open assignee:@me sort:updated-desc"}
	for page := 0; page < maxAssignedPages; page++ {
		var res assignedPage
		if err := c.graphql(ctx, assignedQuery, vars, &res); err != nil {
			return nil, err
		}
		for _, n := range res.Data.Search.Nodes {
			for _, pi := range n.ProjectItems.Nodes {
				if pi.FieldValueByName == nil || !MatchStatus(pi.FieldValueByName.Name, statuses) {
					continue
				}
				items = append(items, WorkItem{
					Repo:      n.Repository.NameWithOwner,
					Number:    n.Number,
					Title:     n.Title,
					URL:       n.URL,
					IsPR:      n.Typename == "PullRequest",
					IsDraft:   n.IsDraft,
					UpdatedAt: n.UpdatedAt,
					Project:   pi.Project.Title,
					Status:    pi.FieldValueByName.Name,
				})
			}
		}
		if !res.Data.Search.PageInfo.HasNextPage || res.Data.Search.PageInfo.EndCursor == "" {
			break
		}
		vars["after"] = res.Data.Search.PageInfo.EndCursor
	}
	return items, nil
}

// MatchStatus reports whether a project status column counts as one of the
// wanted statuses. The comparison ignores case and surrounding whitespace, so
// a config of "in progress" matches a board column called "In Progress".
func MatchStatus(status string, wanted []string) bool {
	status = strings.ToLower(strings.TrimSpace(status))
	if status == "" {
		return false
	}
	for _, w := range wanted {
		if status == strings.ToLower(strings.TrimSpace(w)) {
			return true
		}
	}
	return false
}
