package devianter

import (
	"strconv"
	"strings"
	"time"
)

// timeStamp is a time.Time that parses DeviantArt's publication timestamps,
// which are ISO 8601 with no colon in the zone offset and so are rejected by
// encoding/json's default time handling.
type timeStamp struct {
	time.Time
}

func (t *timeStamp) UnmarshalJSON(b []byte) (err error) {
	if b[0] == '"' && b[len(b)-1] == '"' {
		b = b[1 : len(b)-1]
	}
	t.Time, err = time.Parse("2006-01-02T15:04:05-0700", string(b))
	return
}

// Deviation is a single artwork and its metadata: the central type of this
// package. Most endpoints return these, either alone or in slices.
//
// How much of it is populated depends on the endpoint. Search results and
// gallery listings return a shallow Deviation — enough for a thumbnail and a
// title — while [GetDeviation] fills in Extended, with the tags, original file
// details, and description. A zero-valued field usually means the endpoint did
// not send it rather than that the artwork lacks it.
type Deviation struct {
	Title, Url, License string
	PublishedTime       timeStamp
	ID                  int `json:"deviationId"`

	NSFW bool `json:"isMature"`
	AI   bool `json:"isAiGenerated"`
	DD   bool `json:"isDailyDeviation"`

	Author struct {
		Username string
	}
	Stats struct {
		Favourites, Views, Downloads int
	}
	Media    Media
	Extended struct {
		Tags []struct {
			Name string
		}
		OriginalFile struct {
			Type     string
			Width    int
			Height   int
			Filesize int
		}
		DescriptionText Text
		RelatedContent  []struct {
			Deviations []Deviation
		}
	}
	TextContent Text
}

// Media locates a deviation's image files. It is not a usable URL on its own:
// the pieces have to be assembled, and the result signed with a token. Pass it
// to [UrlFromMedia] rather than building the URL by hand.
type Media struct {
	BaseUri string
	Name    string `json:"prettyName"`
	Token   []string
	// Types are the renditions available (thumbnails, preview, "fullview"), each
	// with its own dimensions.
	Types []struct {
		T    string
		H, W int
	}
}

// Text is a block of user-written text — a description, a comment, a group's
// about page.
//
// Markup is a rich document rather than a string of prose, in whichever format
// DeviantArt stored it: tiptap JSON on anything recent (Type is "tiptap"),
// Draft.js JSON on older bodies, or plain HTML on the oldest. The functions
// returning a Text generally flatten it to plain text in a neighbouring field,
// which is what most callers want; read Markup itself for the formatting,
// images, and links that flattening discards.
type Text struct {
	Excerpt string
	Html    struct {
		Markup, Type string
	}
}

// Post is a deviation together with its comment metadata, as returned by
// [GetDeviation]. IMG and Description are conveniences that GetDeviation derives
// from the Deviation, so callers need not assemble a URL or flatten a rich-text
// document themselves. Description is empty for the many deviations that have
// none.
//
// Comments holds only a total and a cursor. To retrieve the comments, pass them
// to [GetComments] with type 1.
type Post struct {
	Deviation Deviation
	Comments  struct {
		Total  int
		Cursor string
	}

	ParsedComments []struct {
		Author         string
		Posted         timeStamp
		Replies, Likes int
	}

	IMG, Description string
}

// UrlFromMedia assembles a usable, token-signed image URL from a [Media], along
// with the filename DeviantArt would serve it under. It selects the "fullview"
// rendition and returns empty strings if the media has none.
//
// An optional thumb argument scales the request down towards that many pixels
// per side, for fetching a smaller copy than the original. GIFs and very large
// images (beyond roughly 33 megapixels) are returned at their original URL
// without resizing, as DeviantArt's resizer refuses them.
func UrlFromMedia(m Media, thumb ...int) (urlParsed, wellFormattedFilename string) {
	var url strings.Builder

	for _, t := range m.Types {
		if t.T != "fullview" {
			continue
		}
		url.WriteString(m.BaseUri)
		if path, name, ok := fitPath(m, t.W, t.H, thumb); ok {
			wellFormattedFilename = name
			url.WriteString(path)
		}
		if len(m.Token) > 0 {
			url.WriteString("?token=")
			url.WriteString(m.Token[0])
		}
	}

	urlParsed = url.String()

	return
}

// fitPath returns the resize path for a w by h rendition of m and the filename
// it is served under. ok is false for GIFs and for images DeviantArt's resizer
// refuses.
func fitPath(m Media, w, h int, thumb []int) (path, name string, ok bool) {
	l := len(m.BaseUri)
	if l == 0 || m.BaseUri[l-3:] == "gif" || w*h >= 33177600 {
		return "", "", false
	}
	if len(thumb) != 0 {
		w = shrinkToward(w, thumb[0])
		h = shrinkToward(h, thumb[0])
	}
	name = m.Name + m.BaseUri[l-4:]
	return "/v1/fit/w_" + strconv.Itoa(w) + ",h_" + strconv.Itoa(h) + "/" + name, name, true
}

// shrinkToward steps v down by to while it stays above to.
func shrinkToward(v, to int) int {
	for x := v; x > to; x -= to {
		v = x
	}
	return v
}

// GetDeviation retrieves a single deviation by its numeric ID and its author's
// username. Both are required: the endpoint will not resolve an ID alone. They
// appear in a deviation's page URL, which ends in a slug of the form
// title-by-author-123456789.
//
// The returned Post has its IMG and Description already derived, and its
// Deviation is fully populated, including Extended.
func GetDeviation(id string, user string) (st Post, err Error) {
	err = ujson(
		"dadeviation/init?deviationid="+id+"&username="+user+"&type=art&include_session=false&expand=deviation.related&preload=true",
		&st,
	)

	st.IMG, _ = UrlFromMedia(st.Deviation.Media)

	// The description lives in Extended.DescriptionText on the great majority of
	// deviations; TextContent carries it on only a small minority. Prefer the
	// former and fall back, since either may be the populated one.
	desc := st.Deviation.Extended.DescriptionText.Html.Markup
	if desc == "" {
		desc = st.Deviation.TextContent.Html.Markup
	}
	st.Description = flattenMarkup(desc)

	return
}
