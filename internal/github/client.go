package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"cyronmanager/internal/types"
)

type Client struct {
	http  *http.Client
	token string
}

func New(token string) *Client {
	if token == "" {
		token = os.Getenv("GITHUB_TOKEN")
	}
	return &Client{
		http:  &http.Client{Timeout: 25 * time.Second},
		token: token,
	}
}

func (c *Client) SetToken(token string) { c.token = token }

func (c *Client) get(ctx context.Context, rawURL string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "CYRON/1.0")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var apiErr struct {
			Message string `json:"message"`
			Errors  []struct {
				Message string `json:"message"`
				Field   string `json:"field"`
			} `json:"errors"`
		}
		_ = json.Unmarshal(body, &apiErr)
		message := apiErr.Message
		if message == "" {
			message = resp.Status
		}
		for _, item := range apiErr.Errors {
			if item.Message == "" {
				continue
			}
			if item.Field != "" {
				message += ": " + item.Message + " (" + item.Field + ")"
			} else {
				message += ": " + item.Message
			}
		}
		return fmt.Errorf("GitHub API (%d): %s", resp.StatusCode, message)
	}
	return json.Unmarshal(body, out)
}

func (c *Client) SearchRepos(ctx context.Context, query string) ([]types.GitHubRepo, error) {
	query = strings.TrimSpace(query)
	if len(query) < 2 {
		return nil, errors.New("search query is too short")
	}
	if len(query) > 120 {
		query = query[:120]
	}
	u := "https://api.github.com/search/repositories?q=" + url.QueryEscape(query) + "&sort=stars&order=desc&per_page=20"
	var payload struct {
		Items []struct {
			FullName      string `json:"full_name"`
			Name          string `json:"name"`
			Description   string `json:"description"`
			Stargazers    int    `json:"stargazers_count"`
			Language      string `json:"language"`
			DefaultBranch string `json:"default_branch"`
			HTMLURL       string `json:"html_url"`
			UpdatedAt     string `json:"updated_at"`
			Owner         struct {
				Login string `json:"login"`
			} `json:"owner"`
		} `json:"items"`
	}
	if err := c.get(ctx, u, &payload); err != nil {
		return nil, err
	}
	out := make([]types.GitHubRepo, 0, len(payload.Items))
	for _, item := range payload.Items {
		out = append(out, types.GitHubRepo{
			FullName:      item.FullName,
			Name:          item.Name,
			Owner:         item.Owner.Login,
			Description:   item.Description,
			Stars:         item.Stargazers,
			Language:      item.Language,
			DefaultBranch: item.DefaultBranch,
			HTMLURL:       item.HTMLURL,
			UpdatedAt:     item.UpdatedAt,
		})
	}
	return out, nil
}

func (c *Client) Releases(ctx context.Context, owner, repo string) ([]types.GitHubRelease, error) {
	if err := validateRepoPart(owner); err != nil {
		return nil, err
	}
	if err := validateRepoPart(repo); err != nil {
		return nil, err
	}
	u := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases?per_page=8", url.PathEscape(owner), url.PathEscape(repo))
	var payload []struct {
		ID          int64  `json:"id"`
		TagName     string `json:"tag_name"`
		Name        string `json:"name"`
		PublishedAt string `json:"published_at"`
		HTMLURL     string `json:"html_url"`
		Assets      []struct {
			ID                 int64  `json:"id"`
			Name               string `json:"name"`
			Size               int64  `json:"size"`
			BrowserDownloadURL string `json:"browser_download_url"`
			ContentType        string `json:"content_type"`
		} `json:"assets"`
	}
	if err := c.get(ctx, u, &payload); err != nil {
		return nil, err
	}
	out := make([]types.GitHubRelease, 0, len(payload))
	for _, rel := range payload {
		r := types.GitHubRelease{
			ID:          rel.ID,
			Tag:         rel.TagName,
			Name:        rel.Name,
			PublishedAt: rel.PublishedAt,
			HTMLURL:     rel.HTMLURL,
			Assets:      make([]types.GitHubAsset, 0, len(rel.Assets)),
		}
		for _, asset := range rel.Assets {
			r.Assets = append(r.Assets, types.GitHubAsset{
				ID:          asset.ID,
				Name:        asset.Name,
				Size:        asset.Size,
				DownloadURL: asset.BrowserDownloadURL,
				ContentType: asset.ContentType,
			})
		}
		out = append(out, r)
	}
	return out, nil
}

func (c *Client) RepoArchive(ctx context.Context, owner, repo, branch string) (string, string, error) {
	if err := validateRepoPart(owner); err != nil {
		return "", "", err
	}
	if err := validateRepoPart(repo); err != nil {
		return "", "", err
	}
	branch = strings.TrimSpace(branch)
	if branch == "" {
		branch = "main"
	}
	ref, err := archiveRef(branch)
	if err != nil {
		return "", "", err
	}
	downloadURL := fmt.Sprintf("https://codeload.github.com/%s/%s/zip/refs/heads/%s", url.PathEscape(owner), url.PathEscape(repo), ref)
	return downloadURL, fmt.Sprintf("%s-%s-%s.zip", owner, repo, strings.ReplaceAll(branch, "/", "-")), nil
}

func (c *Client) TagArchive(ctx context.Context, owner, repo, tag string) (string, string, error) {
	if err := validateRepoPart(owner); err != nil {
		return "", "", err
	}
	if err := validateRepoPart(repo); err != nil {
		return "", "", err
	}
	tag = strings.TrimSpace(tag)
	if tag == "" || len(tag) > 120 {
		return "", "", errors.New("invalid tag")
	}
	ref, err := archiveRef(tag)
	if err != nil {
		return "", "", err
	}
	downloadURL := fmt.Sprintf("https://codeload.github.com/%s/%s/zip/refs/tags/%s", url.PathEscape(owner), url.PathEscape(repo), ref)
	return downloadURL, fmt.Sprintf("%s-%s-%s.zip", owner, repo, strings.ReplaceAll(tag, "/", "-")), nil
}

func archiveRef(value string) (string, error) {
	parts := strings.Split(value, "/")
	escaped := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || len(part) > 100 {
			return "", errors.New("invalid ref")
		}
		for _, r := range part {
			if r < 0x20 || r == 0x7f || r == '\\' || r == '%' {
				return "", errors.New("invalid ref")
			}
		}
		escaped = append(escaped, url.PathEscape(part))
	}
	return strings.Join(escaped, "/"), nil
}

func ValidateAsset(asset types.GitHubAsset) error {
	if asset.DownloadURL == "" {
		return errors.New("asset has no download URL")
	}
	u, err := url.Parse(asset.DownloadURL)
	if err != nil || u.Scheme != "https" {
		return errors.New("invalid asset URL")
	}
	if asset.Size <= 0 || asset.Size > 8*1024*1024*1024 {
		return errors.New("asset size is not supported")
	}
	return nil
}

func validateRepoPart(value string) error {
	if value == "" || len(value) > 100 {
		return errors.New("invalid repository")
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			continue
		}
		return errors.New("invalid repository")
	}
	return nil
}

func FormatSize(bytes int64) string {
	if bytes <= 0 {
		return "0"
	}
	units := []string{"B", "KB", "MB", "GB", "TB"}
	f := float64(bytes)
	i := 0
	for f >= 1024 && i < len(units)-1 {
		f /= 1024
		i++
	}
	return strconv.FormatFloat(f, 'f', 1, 64) + " " + units[i]
}
