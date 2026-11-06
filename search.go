package nexusmods

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// MaxSearchCount is the most mods one search returns: asking for more still answers this many.
const MaxSearchCount = 80

// ModSort orders a search. Each value is a field of Nexus's v2 ModsSort; the zero value sorts by endorsements.
type ModSort string

// The sorts Search offers, all descending except SortName.
const (
	SortEndorsements ModSort = "endorsements"
	SortDownloads    ModSort = "downloads"
	SortUpdated      ModSort = "updated"
	SortCreated      ModSort = "created"
	SortName         ModSort = "name"
)

func (s ModSort) clause() string {
	switch s {
	case SortEndorsements:
		return "endorsements:{direction:DESC}"
	case SortDownloads:
		return "downloads:{direction:DESC}"
	case SortUpdated:
		return "updatedAt:{direction:DESC}"
	case SortCreated:
		return "createdAt:{direction:DESC}"
	case SortName:
		return "name:{direction:ASC}"
	}
	return "endorsements:{direction:DESC}"
}

// SearchQuery selects one page of a game's mods.
type SearchQuery struct {
	// Domain is the game's domain, such as "stardewvalley". Required.
	Domain string
	// Text matches mod names as a wildcard; blank lists the game's mods unfiltered.
	Text string
	Sort ModSort
	// Categories keeps mods in any one of these categories; empty keeps every category.
	Categories []string
	// ExcludeCategories drops mods in any of these categories.
	ExcludeCategories []string
	// Count is the page size, capped at MaxSearchCount; zero or less means MaxSearchCount.
	Count int
	// Offset is how many mods to skip.
	Offset int
}

// SearchMod is one search hit. SizeKB is the mod's main file size.
type SearchMod struct {
	ModID        int       `json:"modId"`
	Name         string    `json:"name"`
	Summary      string    `json:"summary"`
	Author       string    `json:"author"`
	Version      string    `json:"version"`
	Endorsements int       `json:"endorsements"`
	Downloads    int       `json:"downloads"`
	PictureURL   string    `json:"pictureUrl"`
	Updated      time.Time `json:"updatedAt"`
	Adult        bool      `json:"adultContent"`
	SizeKB       int64     `json:"fileSize"`
	Category     string    `json:"-"`
}

// SearchResult is one page of hits and the total number of matches.
type SearchResult struct {
	Total int
	Mods  []SearchMod
}

// quote renders s as a GraphQL string literal: a JSON string is one, so no input can end it or inject a field.
func quote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// filter builds the mods filter. Categories in one filter are ANDed, so "any included category" is an OR of branches,
// one per included category, each also excluding the excluded ones.
func (q SearchQuery) filter() string {
	scope := fmt.Sprintf(`gameDomainName:[{value:%s}]`, quote(q.Domain))
	if strings.TrimSpace(q.Text) != "" {
		scope += fmt.Sprintf(`, name:[{value:%s, op:WILDCARD}]`, quote(q.Text))
	}
	branch := func(include string) string {
		var vals []string
		if include != "" {
			vals = append(vals, fmt.Sprintf(`{value:%s}`, quote(include)))
		}
		for _, c := range q.ExcludeCategories {
			vals = append(vals, fmt.Sprintf(`{value:%s, op:NOT_EQUALS}`, quote(c)))
		}
		if len(vals) == 0 {
			return scope
		}
		return scope + ", categoryName:[" + strings.Join(vals, ", ") + "]"
	}
	if len(q.Categories) == 0 {
		return branch("")
	}
	branches := make([]string, len(q.Categories))
	for i, c := range q.Categories {
		branches[i] = "{" + branch(c) + "}"
	}
	return "op:OR, filter:[" + strings.Join(branches, ", ") + "]"
}

// Search lists one page of a game's mods through the v2 GraphQL API, in q.Sort order.
func (c *Client) Search(ctx context.Context, q SearchQuery) (SearchResult, error) {
	if q.Domain == "" {
		return SearchResult{}, errors.New("nexus: search needs a game domain")
	}
	count := q.Count
	if count <= 0 || count > MaxSearchCount {
		count = MaxSearchCount
	}
	query := fmt.Sprintf(
		`{ mods(filter:{%s}, sort:[{%s}], count: %d, offset: %d) { totalCount nodes { modId name summary author version endorsements downloads pictureUrl updatedAt adultContent fileSize modCategory { name } } } }`,
		q.filter(), q.Sort.clause(), count, max(q.Offset, 0),
	)
	body, err := c.graphql(ctx, map[string]string{"query": query})
	if err != nil {
		return SearchResult{}, err
	}
	var raw struct {
		Data struct {
			Mods struct {
				TotalCount int `json:"totalCount"`
				Nodes      []struct {
					SearchMod
					ModCategory struct {
						Name string `json:"name"`
					} `json:"modCategory"`
				} `json:"nodes"`
			} `json:"mods"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return SearchResult{}, err
	}
	if len(raw.Data.Mods.Nodes) == 0 && len(raw.Errors) > 0 {
		return SearchResult{}, errors.New("nexus: " + raw.Errors[0].Message)
	}
	res := SearchResult{Total: raw.Data.Mods.TotalCount, Mods: make([]SearchMod, len(raw.Data.Mods.Nodes))}
	for i, n := range raw.Data.Mods.Nodes {
		res.Mods[i] = n.SearchMod
		res.Mods[i].Category = n.ModCategory.Name
		res.Mods[i].Updated = res.Mods[i].Updated.UTC()
	}
	return res, nil
}
