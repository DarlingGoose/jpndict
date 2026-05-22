package jpndict

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/DarlingGoose/jpndict/audio"
	"github.com/DarlingGoose/jpndict/mdict"
	"github.com/PuerkitoBio/goquery"
	"golang.org/x/net/html"
)

const defaultZip = "https://github.com/stephenmk/stephenmk.github.io/releases/latest/download/jitendex-mdict.zip"

var _ Dictonary = &JiTenDex{}

type JiTenDex struct {
	zipFilePath  string
	Dir          string
	AudioEnabled bool
	m            *mdict.MDX
	a            audio.AudioProvider
}

func (j *JiTenDex) SearchAll(data Search) ([]*Response, error) {
	return j.searchAll(data, SearchAllOptions{LongestOnly: true})
}

type SearchAllOptions struct {
	LongestOnly bool
}

// SearchAllWithOptions allows callers to choose between all substring matches
// and the longest non-overlapping matches.
func (j *JiTenDex) SearchAllWithOptions(data Search, opts SearchAllOptions) ([]*Response, error) {
	return j.searchAll(data, opts)
}

func (j *JiTenDex) searchAll(data Search, opts SearchAllOptions) ([]*Response, error) {
	if j.m == nil {
		return nil, fmt.Errorf("dictionary not loaded: call Download first")
	}

	keys := searchAllKeys(data, j.m.HasKey, opts)
	if len(keys) == 0 {
		return nil, fmt.Errorf("not found: %s", data.Text)
	}

	responses := make([]*Response, 0, len(keys))
	for _, key := range keys {
		resp, err := j.Search(Search{Text: key, WithAudio: data.WithAudio})
		if err != nil {
			return nil, err
		}
		resp.Query = key
		responses = append(responses, resp)
	}

	return responses, nil
}

func searchAllKeys(input Search, hasKey func(string) bool, opts SearchAllOptions) []string {
	parts := phraseTerms(input.Text)
	if len(parts) <= 1 {
		if opts.LongestOnly {
			return longestDictionaryMatches(input.Text, hasKey)
		}
		return allDictionaryMatches(input.Text, hasKey)
	}

	keys := make([]string, 0, len(parts))
	seen := map[string]struct{}{}
	for _, part := range parts {
		matches := searchPhraseTerm(part, hasKey, opts)
		if len(matches) == 0 {
			return nil
		}
		for _, match := range matches {
			if _, ok := seen[match]; ok {
				continue
			}
			keys = append(keys, match)
			seen[match] = struct{}{}
		}
	}
	return keys
}

func searchPhraseTerm(term string, hasKey func(string) bool, opts SearchAllOptions) []string {
	term = strings.TrimSpace(term)
	if term == "" || hasKey == nil {
		return nil
	}
	if hasKey(term) {
		return []string{term}
	}
	if opts.LongestOnly {
		return longestDictionaryMatches(term, hasKey)
	}
	return allDictionaryMatches(term, hasKey)
}

func phraseTerms(input string) []string {
	return strings.FieldsFunc(strings.TrimSpace(input), func(r rune) bool {
		if unicode.IsSpace(r) {
			return true
		}
		switch r {
		case '、', '。', '，', '．', ',', '.', '!', '?', '！', '？', '・', ';', '；', ':', '：':
			return true
		default:
			return false
		}
	})
}

func NewJiTenDex(dir string, zipFilePath string, audioEnabled bool) (Dictonary, error) {
	if dir == "" {
		cache, err := os.UserCacheDir()
		if err != nil {
			return nil, err
		}
		dir = filepath.Join(cache, "jpndict")
	}
	if zipFilePath == "" {
		zipFilePath = defaultZip
	}
	var a audio.AudioProvider
	var err error
	if audioEnabled {
		a, err = audio.NewLanguagePod101(filepath.Join(dir, "audio"))
		if err != nil {
			return nil, err
		}
	}
	return &JiTenDex{
		Dir:          dir,
		zipFilePath:  zipFilePath,
		a:            a,
		AudioEnabled: audioEnabled,
	}, nil

}

func (j *JiTenDex) Download() error {
	mdx := filepath.Join(j.Dir, "jitendex", "jitendex.mdx")
	if _, err := os.Stat(mdx); os.IsNotExist(err) {
		err := DownloadAndUnzip(context.Background(),
			j.zipFilePath,
			j.Dir,
		)
		if err != nil {
			return err
		}
	}

	m, err := mdict.OpenMDX(mdx)
	if err != nil {
		return err
	}
	j.m = m
	return err
}

func (j *JiTenDex) lookupFollow(key string, maxDepth int) ([]byte, string, error) {
	current := key

	for i := 0; i < maxDepth; i++ {
		data, err := j.m.Lookup(current)
		if err != nil {
			return nil, "", err
		}

		s := strings.TrimSpace(strings.TrimRight(string(data), "\x00"))

		if strings.HasPrefix(s, "@@@LINK=") {
			current = strings.TrimSpace(strings.TrimPrefix(s, "@@@LINK="))
			continue
		}

		return data, current, nil
	}

	return nil, "", fmt.Errorf("too many redirects for %s", key)
}

func (j *JiTenDex) Search(search Search) (*Response, error) {
	data, key, err := j.lookupFollow(search.Text, 5)
	if err != nil {
		return nil, err
	}

	htmlText := strings.TrimRight(string(data), "\x00")

	resp := &Response{
		Query: search.Text,
		Key:   key,
		HTML:  htmlText,
		Text:  StripHTML(htmlText),
		Raw:   data,
	}

	entry, err := ParseJitendexHTML(htmlText)
	if err == nil {
		resp.Entry = entry
		if resp.Entry.Pronunciation == nil {
			resp.Entry.Pronunciation = &Pronunciation{}
		}
		if j.AudioEnabled && search.WithAudio {
			aud, err := j.a.GetAudio(context.Background(), search.Text, resp.Entry.Reading)
			if err == nil {
				resp.Entry.Pronunciation.Audio = aud.Path
			} else {
				slog.Error("unable to find audio for word", "word", search)
			}
		}
	}

	return resp, nil
}
func StripHTML(s string) string {
	doc, err := html.Parse(strings.NewReader(s))
	if err != nil {
		return strings.TrimSpace(s)
	}

	var b strings.Builder

	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			txt := strings.TrimSpace(n.Data)
			if txt != "" {
				if b.Len() > 0 {
					b.WriteByte(' ')
				}
				b.WriteString(txt)
			}
		}

		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}

	walk(doc)
	return strings.TrimSpace(b.String())
}

func ParseJitendexHTML(raw string) (*Entry, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(raw))
	if err != nil {
		return nil, err
	}

	entry := &Entry{}

	headline := doc.Find(".headline").First()
	entry.Headword = cleanText(headline.Find(".headword ruby").First().Contents().FilterFunction(func(i int, s *goquery.Selection) bool {
		return goquery.NodeName(s) != "rt"
	}).Text())

	entry.Reading = rubyReading(headline.Find(".headword").First())
	entry.IsPriority = headline.HasClass("priority") || headline.Find(".priority-symbol").Length() > 0

	if p := parsePronunciation(doc); p != nil {
		entry.Pronunciation = p
	}

	doc.Find(".sense-group").Each(func(_ int, sg *goquery.Selection) {
		sense := Sense{}

		sense.Number, _ = sg.Find(".sense").First().Attr("data-sense-number")

		sg.Find(".part-of-speech-info").Each(func(_ int, pos *goquery.Selection) {
			t := cleanText(pos.Text())
			if t != "" {
				sense.PartsOfSpeech = append(sense.PartsOfSpeech, t)
			}
		})

		sg.Find(".glossary .gloss").Each(func(_ int, gloss *goquery.Selection) {
			t := cleanText(gloss.Text())
			if t != "" {
				sense.Glosses = append(sense.Glosses, t)
			}
		})

		sg.Find(".sense-note-content").Each(func(_ int, note *goquery.Selection) {
			t := cleanText(note.Text())
			if t != "" {
				sense.Notes = append(sense.Notes, t)
			}
		})

		sg.Find(".ex-sent").Each(func(_ int, ex *goquery.Selection) {
			e := Example{
				Japanese: cleanText(ex.Find(".ex-sent-ja-content").Text()),
				English:  cleanText(ex.Find(".ex-sent-en-content").Text()),
			}
			if e.Japanese != "" || e.English != "" {
				sense.Examples = append(sense.Examples, e)
			}
		})

		sg.Find(".xref").Each(func(_ int, xr *goquery.Selection) {
			a := xr.Find("a").First()

			href, _ := a.Attr("href")
			targetID, _ := a.Attr("data-target-id")

			x := XRef{
				Word: cleanText(a.Find("ruby").First().Contents().FilterFunction(func(i int, s *goquery.Selection) bool {
					return goquery.NodeName(s) != "rt"
				}).Text()),
				Reading:  rubyReading(a),
				Glossary: cleanText(xr.Find(".xref-glossary").Text()),
				Href:     href,
				TargetID: targetID,
			}

			if x.Word != "" || x.Href != "" {
				sense.XRefs = append(sense.XRefs, x)
			}
		})

		if len(sense.Glosses) > 0 || len(sense.PartsOfSpeech) > 0 {
			entry.Senses = append(entry.Senses, sense)
		}
	})

	doc.Find(".entry-footnotes a").Each(func(_ int, a *goquery.Selection) {
		href, _ := a.Attr("href")
		entry.Links = append(entry.Links, ReferenceLink{
			Label: cleanText(a.Text()),
			Href:  href,
		})
	})

	return entry, nil
}

func allDictionaryMatches(input string, hasKey func(string) bool) []string {
	if hasKey == nil {
		return nil
	}

	runes := []rune(strings.TrimSpace(input))
	if len(runes) == 0 {
		return nil
	}

	type match struct {
		key   string
		start int
		end   int
	}

	matches := make([]match, 0, len(runes))
	seen := map[string]struct{}{}

	for start := 0; start < len(runes); start++ {
		for end := len(runes); end > start; end-- {
			candidate := string(runes[start:end])
			if !hasKey(candidate) {
				continue
			}
			if _, ok := seen[candidate]; ok {
				continue
			}
			matches = append(matches, match{
				key:   candidate,
				start: start,
				end:   end,
			})
			seen[candidate] = struct{}{}
		}
	}

	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].start != matches[j].start {
			return matches[i].start < matches[j].start
		}
		leftLen := matches[i].end - matches[i].start
		rightLen := matches[j].end - matches[j].start
		if leftLen != rightLen {
			return leftLen > rightLen
		}
		return matches[i].key < matches[j].key
	})

	out := make([]string, 0, len(matches))
	for _, m := range matches {
		out = append(out, m.key)
	}
	return out
}

func longestDictionaryMatches(input string, hasKey func(string) bool) []string {
	if hasKey == nil {
		return nil
	}

	runes := []rune(strings.TrimSpace(input))
	if len(runes) == 0 {
		return nil
	}

	matches := make([]string, 0, len(runes))
	seen := map[string]struct{}{}
	for start := 0; start < len(runes); {
		match := ""
		for end := len(runes); end > start; end-- {
			candidate := string(runes[start:end])
			if hasKey(candidate) {
				match = candidate
				break
			}
		}

		if match == "" {
			start++
			continue
		}

		if _, ok := seen[match]; !ok {
			matches = append(matches, match)
			seen[match] = struct{}{}
		}
		start += len([]rune(match))
	}

	return matches
}

func parsePronunciation(doc *goquery.Document) *Pronunciation {
	audio := doc.Find(".audio").First()
	if audio.Length() == 0 {
		return nil
	}

	href, _ := audio.Find("a").First().Attr("href")

	p := &Pronunciation{
		Text:  cleanText(audio.Find(".pronunciation-text").Text()),
		Audio: href,
	}

	if title, ok := audio.Find("img").First().Attr("title"); ok {
		p.Pitch = title
	}

	return p
}

func cleanText(s string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
}

func rubyReading(sel *goquery.Selection) string {
	var parts []string
	sel.Find("rt").Each(func(_ int, rt *goquery.Selection) {
		text := cleanText(rt.Text())
		if text != "" {
			parts = append(parts, text)
		}
	})
	return strings.Join(parts, "")
}
