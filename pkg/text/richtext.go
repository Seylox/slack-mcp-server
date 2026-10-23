package text

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/slack-go/slack"
)

// Slack mrkdwn <-> rich_text conversion.
//
// Message drafts are composer content: Slack stores them as rich_text blocks
// only, never as section blocks or a plain text field. MrkdwnToRichText turns
// the mrkdwn an agent writes (the same markup as message text) into the element
// tree the composer would have produced, so the draft opens in Slack with
// working emoji, mentions, links, styles and lists. RichTextToMrkdwn is the
// inverse, used to show existing drafts as text.

var (
	listItemRegex = regexp.MustCompile(`^([ \t]*)(•|◦|▪|▫|-|\*|(\d+)[.)])[ \t]+(.*)$`)
	emojiRegex    = regexp.MustCompile(`^:([a-z0-9_+'-]+):(?::skin-tone-([2-6]):)?`)
)

var broadcastRanges = map[string]bool{"here": true, "channel": true, "everyone": true}

// numericEmoji are the standard emoji whose names have no letter. Other
// letterless :…: spans are kept as text, so times like 10:30-11:00 survive.
var numericEmoji = map[string]bool{"+1": true, "-1": true, "100": true, "1234": true}

// MrkdwnToRichText converts Slack mrkdwn into one rich_text block.
//
// Supported: paragraphs and line breaks, bullet lists (•, ◦, ▪, -, *) and
// ordered lists (1. or 1)) with nesting by leading whitespace or bullet glyph,
// quotes (> or &gt;), code fences (```), *bold*, _italic_, ~strike~, `code`,
// :emoji: (with ::skin-tone-N:), <@U…>, <#C…>, <!subteam^S…>, <!here>,
// <!channel>, <!everyone>, <url> and <url|text>. The entities &amp;, &lt; and
// &gt; are decoded. Anything else is kept as literal text.
func MrkdwnToRichText(s string) *slack.RichTextBlock {
	s = strings.TrimRight(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	lines := strings.Split(s, "\n")

	var (
		elements []slack.RichTextElement
		para     []string
		quote    []string
		list     *slack.RichTextList
	)
	flushPara := func() {
		if len(para) == 0 {
			return
		}
		joined := strings.Join(para, "\n")
		if strings.TrimSpace(joined) == "" {
			// Blank lines between lists, quotes or code: one newline per line,
			// as the composer stores them.
			elements = append(elements, slack.NewRichTextSection(
				slack.NewRichTextSectionTextElement(strings.Repeat("\n", len(para)), nil)))
		} else {
			elements = append(elements, slack.NewRichTextSection(parseInline(joined, nil)...))
		}
		para = nil
	}
	flushQuote := func() {
		if strings.TrimSpace(strings.Join(quote, "")) == "" {
			quote = nil // a quote of empty lines has nothing to show
		}
		if len(quote) > 0 {
			elements = append(elements, &slack.RichTextQuote{
				Type:     slack.RTEQuote,
				Elements: parseInline(strings.Join(quote, "\n"), nil),
			})
			quote = nil
		}
	}
	flushList := func() {
		if list != nil {
			elements = append(elements, list)
			list = nil
		}
	}
	flushAll := func() {
		flushPara()
		flushQuote()
		flushList()
	}

	for i := 0; i < len(lines); i++ {
		line := lines[i]

		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			flushAll()
			code, next := collectCodeFence(lines, i)
			elements = append(elements, &slack.RichTextPreformatted{
				RichTextSection: slack.RichTextSection{
					Type:     slack.RTEPreformatted,
					Elements: []slack.RichTextSectionElement{slack.NewRichTextSectionTextElement(unescapeEntities(code), nil)},
				},
			})
			i = next
			continue
		}

		if body, ok := quoteLine(line); ok {
			flushPara()
			flushList()
			quote = append(quote, body)
			continue
		}
		flushQuote()

		if m := listItemRegex.FindStringSubmatch(line); m != nil && strings.TrimSpace(m[4]) != "" {
			flushPara()
			style, indent, offset := listItemShape(m[1], m[2], m[3])
			if list == nil || list.Style != style || list.Indent != indent {
				flushList()
				list = slack.NewRichTextList(style, indent)
				list.Offset = offset
			}
			list.Elements = append(list.Elements, slack.NewRichTextSection(parseInline(m[4], nil)...))
			continue
		}
		flushList()

		para = append(para, line)
	}
	flushAll()

	return slack.NewRichTextBlock("", elements...)
}

// collectCodeFence returns the content of the fence starting at lines[start]
// and the index of its closing line. An unclosed fence runs to the end.
func collectCodeFence(lines []string, start int) (string, int) {
	first := strings.TrimPrefix(strings.TrimSpace(lines[start]), "```")
	if before, ok := strings.CutSuffix(first, "```"); ok {
		return before, start
	}
	content := []string{}
	if first != "" {
		content = append(content, first)
	}
	for i := start + 1; i < len(lines); i++ {
		if before, ok := strings.CutSuffix(strings.TrimRight(lines[i], " \t"), "```"); ok {
			if before != "" {
				content = append(content, before)
			}
			return strings.Join(content, "\n"), i
		}
		content = append(content, lines[i])
	}
	return strings.Join(content, "\n"), len(lines) - 1
}

func quoteLine(line string) (string, bool) {
	for _, prefix := range []string{"&gt;", ">"} {
		if rest, ok := strings.CutPrefix(line, prefix); ok {
			return strings.TrimPrefix(rest, " "), true
		}
	}
	return "", false
}

// listItemShape derives a list's style, nesting level and numbering offset.
// The level is the larger of the leading whitespace (four columns per level,
// tabs count four) and the level the bullet glyph implies (◦ = 1, ▪/▫ = 2).
func listItemShape(leading, marker, number string) (slack.RichTextListElementType, int, int) {
	columns := 0
	for _, r := range leading {
		if r == '\t' {
			columns += 4
		} else {
			columns++
		}
	}
	indent := (columns + 2) / 4

	switch marker {
	case "◦":
		indent = max(indent, 1)
	case "▪", "▫":
		indent = max(indent, 2)
	}

	if number != "" {
		offset := 0
		if n, err := strconv.Atoi(number); err == nil && n > 1 {
			offset = n - 1
		}
		return slack.RTEListOrdered, indent, offset
	}
	return slack.RTEListBullet, indent, 0
}

// parseInline converts one span of mrkdwn into rich text section elements.
func parseInline(s string, style *slack.RichTextSectionTextStyle) []slack.RichTextSectionElement {
	var (
		out []slack.RichTextSectionElement
		buf strings.Builder
	)
	flush := func() {
		if buf.Len() > 0 {
			out = append(out, slack.NewRichTextSectionTextElement(buf.String(), copyStyle(style)))
			buf.Reset()
		}
	}

	for i := 0; i < len(s); {
		switch c := s[i]; c {
		case '<':
			if end := strings.IndexByte(s[i+1:], '>'); end >= 0 {
				if el := parseAngleToken(s[i+1:i+1+end], style); el != nil {
					flush()
					out = append(out, el)
					i += end + 2
					continue
				}
			}
		case ':':
			if m := emojiRegex.FindStringSubmatch(s[i:]); m != nil && isEmojiName(m[1]) {
				skinTone := 0
				if m[2] != "" {
					skinTone, _ = strconv.Atoi(m[2])
				}
				flush()
				out = append(out, slack.NewRichTextSectionEmojiElement(m[1], skinTone, copyStyle(style)))
				i += len(m[0])
				continue
			}
		case '&':
			if r, n := decodeEntity(s[i:]); n > 0 {
				buf.WriteString(r)
				i += n
				continue
			}
		case '*', '_', '~', '`':
			if end := closingMarker(s, i); end > 0 {
				flush()
				inner := s[i+1 : end]
				if c == '`' {
					st := withStyle(style, c)
					out = append(out, slack.NewRichTextSectionTextElement(unescapeEntities(inner), st))
				} else {
					out = append(out, parseInline(inner, withStyle(style, c))...)
				}
				i = end + 1
				continue
			}
		}
		buf.WriteByte(s[i])
		i++
	}
	flush()
	return mergeTextElements(out)
}

// closingMarker returns the index of the marker that closes the one at
// s[open], or -1. Following Slack: the opening marker starts a word and is
// followed by a non-space; the closing marker ends a word and is not followed
// by a word character. Angle tokens and code spans are skipped while
// searching, so markers inside URLs do not close a span. A marker next to an
// identical one (as in **) neither opens nor closes.
func closingMarker(s string, open int) int {
	marker := s[open]
	if open > 0 && (s[open-1] == marker || isWordByteBefore(s, open)) {
		return -1
	}
	if open+1 >= len(s) || s[open+1] == marker || isSpaceByte(s[open+1]) {
		return -1
	}
	for j := open + 1; j < len(s); j++ {
		switch {
		case s[j] == '<' && marker != '`':
			if end := strings.IndexByte(s[j+1:], '>'); end >= 0 {
				j += end + 1
				continue
			}
		case s[j] == '`' && marker != '`':
			if end := strings.IndexByte(s[j+1:], '`'); end >= 0 {
				j += end + 1
				continue
			}
		case s[j] == '\n':
			return -1
		case s[j] == marker:
			if isSpaceByte(s[j-1]) || s[j-1] == marker || (j+1 < len(s) && s[j+1] == marker) {
				continue
			}
			if j+1 < len(s) && isWordByteAt(s, j+1) {
				continue
			}
			return j
		}
	}
	return -1
}

func parseAngleToken(token string, style *slack.RichTextSectionTextStyle) slack.RichTextSectionElement {
	target, label, _ := strings.Cut(token, "|")
	switch {
	case strings.HasPrefix(target, "@U"), strings.HasPrefix(target, "@W"):
		if isSlackID(target[1:]) {
			return slack.NewRichTextSectionUserElement(target[1:], copyStyle(style))
		}
	case strings.HasPrefix(target, "#C"), strings.HasPrefix(target, "#G"):
		if isSlackID(target[1:]) {
			return slack.NewRichTextSectionChannelElement(target[1:], copyStyle(style))
		}
	case strings.HasPrefix(target, "!subteam^"):
		if id := strings.TrimPrefix(target, "!subteam^"); isSlackID(id) {
			return slack.NewRichTextSectionUserGroupElement(id)
		}
	case strings.HasPrefix(target, "!"):
		if broadcastRanges[target[1:]] {
			return slack.NewRichTextSectionBroadcastElement(target[1:])
		}
	case isURL(target):
		return slack.NewRichTextSectionLinkElement(unescapeEntities(target), unescapeEntities(label), copyStyle(style))
	}
	return nil
}

func isEmojiName(name string) bool {
	return numericEmoji[name] || strings.IndexFunc(name, unicode.IsLetter) >= 0
}

func isURL(s string) bool {
	if strings.ContainsAny(s, " \t\n") {
		return false
	}
	for _, scheme := range []string{"http://", "https://", "mailto:", "tel:"} {
		if strings.HasPrefix(strings.ToLower(s), scheme) && len(s) > len(scheme) {
			return true
		}
	}
	return false
}

func isSlackID(s string) bool {
	if len(s) < 2 {
		return false
	}
	for _, r := range s {
		if !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

var entities = []struct{ escaped, raw string }{{"&amp;", "&"}, {"&lt;", "<"}, {"&gt;", ">"}}

func decodeEntity(s string) (string, int) {
	for _, e := range entities {
		if strings.HasPrefix(s, e.escaped) {
			return e.raw, len(e.escaped)
		}
	}
	return "", 0
}

func unescapeEntities(s string) string {
	return strings.NewReplacer("&amp;", "&", "&lt;", "<", "&gt;", ">").Replace(s)
}

func escapeEntities(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

func isSpaceByte(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n'
}

// isWordByteBefore reports whether the rune ending right before s[i] is a
// letter or digit (or an underscore, so snake_case stays literal).
func isWordByteBefore(s string, i int) bool {
	r, _ := utf8.DecodeLastRuneInString(s[:i])
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

func isWordByteAt(s string, i int) bool {
	r, _ := utf8.DecodeRuneInString(s[i:])
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

func withStyle(style *slack.RichTextSectionTextStyle, marker byte) *slack.RichTextSectionTextStyle {
	st := slack.RichTextSectionTextStyle{}
	if style != nil {
		st = *style
	}
	switch marker {
	case '*':
		st.Bold = true
	case '_':
		st.Italic = true
	case '~':
		st.Strike = true
	case '`':
		st.Code = true
	}
	return &st
}

func copyStyle(style *slack.RichTextSectionTextStyle) *slack.RichTextSectionTextStyle {
	if style == nil {
		return nil
	}
	st := *style
	return &st
}

func sameStyle(a, b *slack.RichTextSectionTextStyle) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// mergeTextElements joins adjacent text elements that share a style, so
// entity decoding and rejected markers do not fragment the text.
func mergeTextElements(in []slack.RichTextSectionElement) []slack.RichTextSectionElement {
	var out []slack.RichTextSectionElement
	for _, el := range in {
		cur, ok := el.(*slack.RichTextSectionTextElement)
		if ok && len(out) > 0 {
			if prev, ok := out[len(out)-1].(*slack.RichTextSectionTextElement); ok && sameStyle(prev.Style, cur.Style) {
				prev.Text += cur.Text
				continue
			}
		}
		out = append(out, el)
	}
	return out
}

// RichTextToMrkdwn renders the rich_text blocks among blocks as Slack mrkdwn.
// Other block types are skipped. It is the inverse of MrkdwnToRichText for the
// constructs that function produces.
func RichTextToMrkdwn(blocks []slack.Block) string {
	var parts []string
	for _, b := range blocks {
		rt, ok := b.(*slack.RichTextBlock)
		if !ok {
			continue
		}
		for _, el := range rt.Elements {
			parts = append(parts, renderRichTextElement(el))
		}
	}
	return strings.Join(parts, "\n")
}

func renderRichTextElement(el slack.RichTextElement) string {
	switch e := el.(type) {
	case *slack.RichTextSection:
		rendered := renderInline(e.Elements)
		if rendered != "" && strings.Trim(rendered, "\n") == "" {
			// A blank-line section: the newline that joins it to its
			// neighbours already counts as one of its lines.
			return rendered[1:]
		}
		return rendered
	case *slack.RichTextQuote:
		lines := strings.Split(renderInline(e.Elements), "\n")
		for i, l := range lines {
			lines[i] = "> " + l
		}
		return strings.Join(lines, "\n")
	case *slack.RichTextPreformatted:
		return "```\n" + plainText(e.Elements) + "\n```"
	case *slack.RichTextList:
		lines := make([]string, 0, len(e.Elements))
		for i, item := range e.Elements {
			section, ok := item.(*slack.RichTextSection)
			if !ok {
				continue
			}
			lines = append(lines, strings.Repeat("    ", e.Indent)+listMarker(e, i)+" "+renderInline(section.Elements))
		}
		return strings.Join(lines, "\n")
	}
	return ""
}

func listMarker(list *slack.RichTextList, i int) string {
	if list.Style == slack.RTEListOrdered {
		return fmt.Sprintf("%d.", list.Offset+i+1)
	}
	switch list.Indent {
	case 0:
		return "•"
	case 1:
		return "◦"
	default:
		return "▪"
	}
}

// styledPiece is one element's mrkdwn before its style markers are added.
type styledPiece struct {
	text  string
	style *slack.RichTextSectionTextStyle
}

// renderInline renders section elements as mrkdwn. Neighbouring elements with
// the same style share one pair of markers, so "*see <url|docs>*" does not
// come back as "*see *<url|docs>**".
func renderInline(elements []slack.RichTextSectionElement) string {
	var pieces []styledPiece
	for _, el := range elements {
		switch e := el.(type) {
		case *slack.RichTextSectionTextElement:
			pieces = append(pieces, styledPiece{escapeEntities(e.Text), e.Style})
		case *slack.RichTextSectionEmojiElement:
			emoji := ":" + e.Name + ":"
			if e.SkinTone > 1 {
				emoji += fmt.Sprintf(":skin-tone-%d:", e.SkinTone)
			}
			pieces = append(pieces, styledPiece{emoji, e.Style})
		case *slack.RichTextSectionUserElement:
			pieces = append(pieces, styledPiece{"<@" + e.UserID + ">", e.Style})
		case *slack.RichTextSectionChannelElement:
			pieces = append(pieces, styledPiece{"<#" + e.ChannelID + ">", e.Style})
		case *slack.RichTextSectionUserGroupElement:
			pieces = append(pieces, styledPiece{"<!subteam^" + e.UsergroupID + ">", nil})
		case *slack.RichTextSectionBroadcastElement:
			pieces = append(pieces, styledPiece{"<!" + e.Range + ">", nil})
		case *slack.RichTextSectionLinkElement:
			link := "<" + escapeEntities(e.URL)
			if e.Text != "" {
				link += "|" + escapeEntities(e.Text)
			}
			pieces = append(pieces, styledPiece{link + ">", e.Style})
		}
	}

	var b strings.Builder
	for i := 0; i < len(pieces); {
		run := pieces[i].text
		j := i + 1
		for ; j < len(pieces) && sameStyle(pieces[i].style, pieces[j].style); j++ {
			run += pieces[j].text
		}
		b.WriteString(applyStyle(run, pieces[i].style))
		i = j
	}
	return b.String()
}

// applyStyle wraps s in mrkdwn markers. Leading and trailing whitespace stays
// outside the markers, where Slack requires it.
func applyStyle(s string, style *slack.RichTextSectionTextStyle) string {
	if style == nil {
		return s
	}
	core := strings.TrimSpace(s)
	if core == "" {
		return s
	}
	lead := s[:strings.Index(s, core)]
	trail := s[len(lead)+len(core):]
	if style.Code {
		core = "`" + core + "`"
	}
	if style.Strike {
		core = "~" + core + "~"
	}
	if style.Italic {
		core = "_" + core + "_"
	}
	if style.Bold {
		core = "*" + core + "*"
	}
	return lead + core + trail
}

func plainText(elements []slack.RichTextSectionElement) string {
	var b strings.Builder
	for _, el := range elements {
		if t, ok := el.(*slack.RichTextSectionTextElement); ok {
			b.WriteString(t.Text)
		}
	}
	return b.String()
}
