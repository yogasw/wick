package samplepost

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type postResponse struct {
	UserID int    `json:"userId"`
	ID     int    `json:"id"`
	Title  string `json:"title"`
	Body   string `json:"body"`
}

// fetchPost hits {baseURL}/posts/1 and returns the result as markdown.
func fetchPost(ctx context.Context, baseURL string) (string, error) {
	client := &http.Client{Timeout: 10 * time.Second}

	url := strings.TrimRight(baseURL, "/") + "/posts/1"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch post: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}

	var post postResponse
	if err := json.NewDecoder(resp.Body).Decode(&post); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}

	md := fmt.Sprintf("## Post #%d\n\n**Source:** %s\n\n**Title:** %s\n\n**Author ID:** %d\n\n---\n\n%s\n\n---\n\n*Fetched at %s*",
		post.ID,
		url,
		post.Title,
		post.UserID,
		post.Body,
		time.Now().Format("2006-01-02 15:04:05 MST"),
	)

	return md, nil
}
