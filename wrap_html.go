package sparkypmtatracking

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"path"

	"github.com/google/uuid"
	"golang.org/x/net/html"
)

// UniqMessageID returns a SparkPost formatted unique messageID
func UniqMessageID() string {
	uuid := uuid.New()
	uBytes := uuid[0:8]
	u := "00000" + hex.EncodeToString(uBytes)
	return u
}

// WrapperData is used to build the tracking URL
type WrapperData struct {
	Action        string `json:"act"` // carries "c" = click, "o" = open, "i" = initial open
	TargetLinkURL string `json:"t_url"`
	MessageID     string `json:"msg_id"`
	RcptTo        string `json:"rcpt"`
}

// Wrapper information set up as the message is processed
type Wrapper struct {
	URL       url.URL
	messageID string // This info is set up per message
	rcptTo    string // and per recipient
}

// NewWrapper returns a tracker with the persistent info set up from params
func NewWrapper(URL string) (*Wrapper, error) {
	u, err := url.ParseRequestURI(URL)
	if err != nil {
		return nil, err
	}
	if u.RawQuery != "" {
		return nil, errors.New("Can't have query parameters in the tracking URL")
	}
	// Valid - recompose into a canonical form URL, with trailing /
	trk := Wrapper{
		URL: *u,
	}
	return &trk, nil
}

// SetMessageInfo sets the per-message specifics
func (trk *Wrapper) SetMessageInfo(msgID string, rcpt string) {
	trk.messageID = msgID
	trk.rcptTo = rcpt
}

// InitialOpenPixel returns an html fragment with pixel for initial open tracking.
// If there are problems, empty string is returned.
func (trk *Wrapper) InitialOpenPixel() string {
	const pixelPrefix = `<div style="color:transparent;visibility:hidden;opacity:0;font-size:0px;border:0;max-height:1px;width:1px;margin:0px;padding:0px` +
		`;border-width:0px!important;display:none!important;line-height:0px!important;"><img border="0" width="1" height="1" src="`
	const pixelSuffix = `"/></div>` + "\n"
	if trk.URL.String() == "" {
		return ""
	}
	return pixelPrefix + trk.wrap("i", "") + pixelSuffix
}

// OpenPixel returns an html fragment with pixel for bottom open tracking.
// If there are problems, empty string is returned.
func (trk *Wrapper) OpenPixel() string {
	const pixelPrefix = `<img border="0" width="1" height="1" alt="" src="`
	const pixelSuffix = `">` + "\n"
	if trk.URL.String() == "" {
		return ""
	}
	return pixelPrefix + trk.wrap("o", "") + pixelSuffix
}

// WrapURL returns the wrapped, encoded version of the URL for engagement tracking.
// If there are problems, the original unwrapped url is returned.
func (trk *Wrapper) WrapURL(url string) string {
	if trk.URL.String() == "" {
		return url
	}
	return trk.wrap("c", url)
}

func (trk *Wrapper) wrap(action string, targetlink string) string {
	pathData, err := json.Marshal(WrapperData{
		Action:        action,
		TargetLinkURL: targetlink,
		MessageID:     trk.messageID,
		RcptTo:        trk.rcptTo,
	})
	if err != nil {
		return ""
	}
	// Feed the base64 writer from the zlib writer, taking the result as a string
	var b64Z bytes.Buffer
	b64w := base64.NewEncoder(base64.URLEncoding, &b64Z)
	zw := zlib.NewWriter(b64w)
	if _, err = zw.Write(pathData); err != nil {
		return ""
	}
	if err = zw.Close(); err != nil {
		return ""
	}
	pj := path.Join(trk.URL.Path, b64Z.String())
	u := url.URL{ // make a local copy so we don't change the parent
		Scheme: trk.URL.Scheme,
		Host:   trk.URL.Host,
		Path:   pj,
	}
	return u.String()
}

// TrackHTML streams content to w from r (a la io.Copy), adding engagement tracking by wrapping links and inserting open pixel(s).
//Returns count of bytes written and error status
func (trk *Wrapper) TrackHTML(w io.Writer, r io.Reader) (int, error) {
	var count, c int
	var err error
	tok := html.NewTokenizer(r)
	for {
		tokType := tok.Next()
		switch tokType {
		case html.ErrorToken:
			err = tok.Err()
			if err == io.EOF {
				return count, nil //end of the file, normal exit
			}
		case html.StartTagToken:
			token := tok.Token()
			if token.Data == "a" {
				for k, v := range token.Attr {
					if v.Key == "href" {
						// We have an anchor with hyperlink - rewrite the URL back into parent structure
						token.Attr[k].Val = trk.WrapURL(v.Val)
					}
				}
				c, err = io.WriteString(w, token.String())
				count += c
			} else {
				if token.Data == "body" {
					c, err = w.Write(tok.Raw())
					count += c
					c, err = io.WriteString(w, trk.InitialOpenPixel()) // top tracking pixel
					count += c
				} else {
					c, err = w.Write(tok.Raw()) // pass through
					count += c
				}
			}
		case html.EndTagToken:
			token := tok.Token()
			if token.Data == "body" {
				c, err = io.WriteString(w, trk.OpenPixel()) // bottom tracking pixel
				count += c
				c, err = w.Write(tok.Raw())
				count += c
			} else {
				c, err = w.Write(tok.Raw()) // pass through
				count += c
			}
		default:
			c, err = w.Write(tok.Raw()) // pass through
			count += c
		}
		if err != nil {
			return count, err // Catches errors that may arise from the Write & WriteString calls
		}
	}
}
