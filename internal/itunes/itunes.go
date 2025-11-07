package itunes

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Client interacts with the iTunes Search API.
type Client struct {
	httpClient *http.Client
	baseURL    string
}

// NewClient creates a client using the provided HTTP client. The baseURL can be
// overridden for testing; if empty the public API endpoint is used.
func NewClient(httpClient *http.Client, baseURL string) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	if strings.TrimSpace(baseURL) == "" {
		baseURL = "https://itunes.apple.com"
	}
	return &Client{httpClient: httpClient, baseURL: strings.TrimRight(baseURL, "/")}
}

// Podcast represents a podcast returned by the iTunes API.
type Podcast struct {
	ID              string
	Title           string
	Author          string
	FeedURL         string
	Artwork         string
	Genre           string
	Country         string
	Language        string
	Description     string
	LongDescription string
}

// Search queries the API for podcasts matching the supplied term.
func (c *Client) Search(ctx context.Context, term string, limit int) ([]Podcast, error) {
	if strings.TrimSpace(term) == "" {
		return nil, fmt.Errorf("search term cannot be empty")
	}
	if limit <= 0 {
		limit = 10
	}

	endpoint, err := url.Parse(c.baseURL + "/search")
	if err != nil {
		return nil, err
	}
	q := endpoint.Query()
	q.Set("media", "podcast")
	q.Set("term", term)
	q.Set("limit", strconv.Itoa(limit))
	endpoint.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("itunes search: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("itunes search failed: %s", resp.Status)
	}

	var payload searchResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode search response: %w", err)
	}

	results := make([]Podcast, 0, len(payload.Results))
	for _, item := range payload.Results {
		id := strconv.FormatInt(item.CollectionID, 10)
		results = append(results, Podcast{
			ID:              id,
			Title:           item.CollectionName,
			Author:          item.ArtistName,
			FeedURL:         item.FeedURL,
			Artwork:         item.ArtworkURL100,
			Genre:           item.PrimaryGenreName,
			Country:         item.Country,
			Language:        item.Language,
			Description:     item.Description,
			LongDescription: item.LongDescription,
		})
	}
	return results, nil
}

// LookupPodcast retrieves metadata for a single podcast by its collection ID.
func (c *Client) LookupPodcast(ctx context.Context, id string) (Podcast, error) {
	endpoint, err := url.Parse(c.baseURL + "/lookup")
	if err != nil {
		return Podcast{}, err
	}
	q := endpoint.Query()
	q.Set("id", id)
	endpoint.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return Podcast{}, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return Podcast{}, fmt.Errorf("itunes lookup: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return Podcast{}, fmt.Errorf("itunes lookup failed: %s", resp.Status)
	}

	var payload lookupResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return Podcast{}, fmt.Errorf("decode lookup response: %w", err)
	}
	if len(payload.Results) == 0 {
		return Podcast{}, fmt.Errorf("podcast not found")
	}

	item := payload.Results[0]
	idInt := strconv.FormatInt(item.CollectionID, 10)
	return Podcast{
		ID:              idInt,
		Title:           item.CollectionName,
		Author:          item.ArtistName,
		FeedURL:         item.FeedURL,
		Artwork:         item.ArtworkURL100,
		Genre:           item.PrimaryGenreName,
		Country:         item.Country,
		Language:        item.Language,
		Description:     item.Description,
		LongDescription: item.LongDescription,
	}, nil
}

type searchResponse struct {
	Results []podcastResult `json:"results"`
}

type lookupResponse struct {
	Results []podcastResult `json:"results"`
}

type podcastResult struct {
	CollectionID     int64  `json:"collectionId"`
	CollectionName   string `json:"collectionName"`
	ArtistName       string `json:"artistName"`
	FeedURL          string `json:"feedUrl"`
	ArtworkURL100    string `json:"artworkUrl100"`
	PrimaryGenreName string `json:"primaryGenreName"`
	Country          string `json:"country"`
	Language         string `json:"language"`
	Description      string `json:"description"`
	LongDescription  string `json:"longDescription"`
}
