package feeds

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Podcast describes metadata for a podcast feed.
type Podcast struct {
	Title       string
	Description string
}

// Episode captures parsed feed episode information.
type Episode struct {
	ID          string
	Title       string
	Description string
	PublishedAt time.Time
	Enclosure   string
}

// Fetch retrieves and parses an RSS/Atom feed.
func Fetch(ctx context.Context, client *http.Client, url string) (Podcast, []Episode, error) {
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Podcast{}, nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return Podcast{}, nil, fmt.Errorf("fetch feed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return Podcast{}, nil, fmt.Errorf("fetch feed failed: %s", resp.Status)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return Podcast{}, nil, fmt.Errorf("read feed: %w", err)
	}

	var rss rssDocument
	if err := xml.Unmarshal(data, &rss); err != nil {
		return Podcast{}, nil, fmt.Errorf("parse feed: %w", err)
	}

	episodes := make([]Episode, 0, len(rss.Channel.Items))
	for _, item := range rss.Channel.Items {
		guid := strings.TrimSpace(item.GUID.Value)
		if guid == "" {
			guid = strings.TrimSpace(item.Enclosure.URL)
		}
		if guid == "" {
			guid = strings.TrimSpace(item.Link)
		}
		if guid == "" {
			guid = fmt.Sprintf("%s:%s", rss.Channel.Title, item.Title)
		}

		published, _ := parseTime(item.PubDate)
		episodes = append(episodes, Episode{
			ID:          guid,
			Title:       strings.TrimSpace(item.Title),
			Description: strings.TrimSpace(item.Description),
			PublishedAt: published,
			Enclosure:   strings.TrimSpace(item.Enclosure.URL),
		})
	}

	return Podcast{
		Title:       strings.TrimSpace(rss.Channel.Title),
		Description: strings.TrimSpace(rss.Channel.Description),
	}, episodes, nil
}

func parseTime(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, fmt.Errorf("empty time")
	}
	layouts := []string{
		time.RFC1123Z,
		time.RFC1123,
		time.RFC822Z,
		time.RFC822,
		time.RFC3339,
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, value); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unable to parse time: %s", value)
}

type rssDocument struct {
	XMLName xml.Name   `xml:"rss"`
	Channel rssChannel `xml:"channel"`
}

type rssChannel struct {
	Title       string    `xml:"title"`
	Description string    `xml:"description"`
	Items       []rssItem `xml:"item"`
}

type rssItem struct {
	GUID        rssGUID      `xml:"guid"`
	Title       string       `xml:"title"`
	Description string       `xml:"description"`
	Link        string       `xml:"link"`
	PubDate     string       `xml:"pubDate"`
	Enclosure   rssEnclosure `xml:"enclosure"`
}

type rssGUID struct {
	Value string `xml:",chardata"`
}

type rssEnclosure struct {
	URL    string `xml:"url,attr"`
	Length string `xml:"length,attr"`
	Type   string `xml:"type,attr"`
}
