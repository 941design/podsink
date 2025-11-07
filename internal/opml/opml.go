package opml

import (
	"encoding/xml"
	"fmt"
	"io"
	"time"
)

// OPML represents the root OPML document structure.
type OPML struct {
	XMLName xml.Name `xml:"opml"`
	Version string   `xml:"version,attr"`
	Head    Head     `xml:"head"`
	Body    Body     `xml:"body"`
}

// Head contains metadata about the OPML document.
type Head struct {
	Title       string `xml:"title,omitempty"`
	DateCreated string `xml:"dateCreated,omitempty"`
}

// Body contains the list of outlines (subscriptions).
type Body struct {
	Outlines []Outline `xml:"outline"`
}

// Outline represents a single podcast subscription.
type Outline struct {
	Type    string `xml:"type,attr"`
	Text    string `xml:"text,attr"`
	Title   string `xml:"title,attr,omitempty"`
	XMLURL  string `xml:"xmlUrl,attr"`
	HTMLURL string `xml:"htmlUrl,attr,omitempty"`
}

// Subscription represents a parsed podcast subscription from OPML.
type Subscription struct {
	Title   string
	FeedURL string
}

// Export writes subscriptions to an OPML file.
func Export(w io.Writer, subscriptions []Subscription) error {
	doc := OPML{
		Version: "2.0",
		Head: Head{
			Title:       "Podsink Subscriptions",
			DateCreated: time.Now().UTC().Format(time.RFC1123Z),
		},
		Body: Body{
			Outlines: make([]Outline, 0, len(subscriptions)),
		},
	}

	for _, sub := range subscriptions {
		doc.Body.Outlines = append(doc.Body.Outlines, Outline{
			Type:   "rss",
			Text:   sub.Title,
			Title:  sub.Title,
			XMLURL: sub.FeedURL,
		})
	}

	encoder := xml.NewEncoder(w)
	encoder.Indent("", "  ")
	if err := encoder.Encode(doc); err != nil {
		return fmt.Errorf("encode OPML: %w", err)
	}

	return nil
}

// Import parses OPML data and returns a list of subscriptions.
func Import(r io.Reader) ([]Subscription, error) {
	var doc OPML
	decoder := xml.NewDecoder(r)
	if err := decoder.Decode(&doc); err != nil {
		return nil, fmt.Errorf("decode OPML: %w", err)
	}

	subscriptions := make([]Subscription, 0, len(doc.Body.Outlines))
	for _, outline := range doc.Body.Outlines {
		if outline.XMLURL == "" {
			continue
		}
		title := outline.Title
		if title == "" {
			title = outline.Text
		}
		subscriptions = append(subscriptions, Subscription{
			Title:   title,
			FeedURL: outline.XMLURL,
		})
	}

	return subscriptions, nil
}
