package text

import (
	"encoding/json"
	"testing"

	"github.com/slack-go/slack"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// inlineJSON returns the elements of the single section MrkdwnToRichText
// produces for a one-line input.
func inlineJSON(t *testing.T, s string) string {
	t.Helper()
	block := MrkdwnToRichText(s)
	require.Len(t, block.Elements, 1, "input %q", s)
	section, ok := block.Elements[0].(*slack.RichTextSection)
	require.True(t, ok, "input %q: expected a rich_text_section, got %T", s, block.Elements[0])
	raw, err := json.Marshal(section.Elements)
	require.NoError(t, err)
	return string(raw)
}

func TestUnitMrkdwnToRichTextInline(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain text", "hello world",
			`[{"type":"text","text":"hello world"}]`},
		{"styles", "*b* _i_ ~s~ `c`",
			`[{"type":"text","text":"b","style":{"bold":true}},{"type":"text","text":" "},
			  {"type":"text","text":"i","style":{"italic":true}},{"type":"text","text":" "},
			  {"type":"text","text":"s","style":{"strike":true}},{"type":"text","text":" "},
			  {"type":"text","text":"c","style":{"code":true}}]`},
		{"nested styles", "*bold _both_*",
			`[{"type":"text","text":"bold ","style":{"bold":true}},
			  {"type":"text","text":"both","style":{"bold":true,"italic":true}}]`},
		{"code span is literal", "`*not bold* :tada:`",
			`[{"type":"text","text":"*not bold* :tada:","style":{"code":true}}]`},
		{"emoji", "done :tada:",
			`[{"type":"text","text":"done "},{"type":"emoji","name":"tada"}]`},
		{"emoji with skin tone", ":wave::skin-tone-3:",
			`[{"type":"emoji","name":"wave","skin_tone":3}]`},
		{"numeric emoji", ":+1: :100:",
			`[{"type":"emoji","name":"+1"},{"type":"text","text":" "},{"type":"emoji","name":"100"}]`},
		{"times are not emoji", "10:30-11:00",
			`[{"type":"text","text":"10:30-11:00"}]`},
		{"colon in prose", "Note: see :x: here",
			`[{"type":"text","text":"Note: see "},{"type":"emoji","name":"x"},{"type":"text","text":" here"}]`},
		{"snake_case stays literal", "use snake_case_names",
			`[{"type":"text","text":"use snake_case_names"}]`},
		{"mentions", "<@U0TEST1> <@W0TEST2|alice> <#C0TEST3> <#C0TEST4|general>",
			`[{"type":"user","user_id":"U0TEST1"},{"type":"text","text":" "},
			  {"type":"user","user_id":"W0TEST2"},{"type":"text","text":" "},
			  {"type":"channel","channel_id":"C0TEST3"},{"type":"text","text":" "},
			  {"type":"channel","channel_id":"C0TEST4"}]`},
		{"user group and broadcast", "<!subteam^S0TEST5|@team> <!here> <!channel>",
			`[{"type":"usergroup","usergroup_id":"S0TEST5"},{"type":"text","text":" "},
			  {"type":"broadcast","range":"here"},{"type":"text","text":" "},
			  {"type":"broadcast","range":"channel"}]`},
		{"links", "<https://example.com/a?x=1&amp;y=2|the docs> and <https://example.com>",
			`[{"type":"link","url":"https://example.com/a?x=1&y=2","text":"the docs"},
			  {"type":"text","text":" and "},{"type":"link","url":"https://example.com"}]`},
		{"styled link", "*<https://example.com|bold link>*",
			`[{"type":"link","url":"https://example.com","text":"bold link","style":{"bold":true}}]`},
		{"marker inside a URL does not close a span", "_see <https://example.com/a_b>_",
			`[{"type":"text","text":"see ","style":{"italic":true}},
			  {"type":"link","url":"https://example.com/a_b","style":{"italic":true}}]`},
		{"unknown angle token is literal", "a <not a link> b",
			`[{"type":"text","text":"a <not a link> b"}]`},
		{"entities are decoded", "a &amp; b &lt;c&gt;",
			`[{"type":"text","text":"a & b <c>"}]`},
		{"unclosed and doubled markers are literal", "*open and **double**",
			`[{"type":"text","text":"*open and **double**"}]`},
		{"marker followed by space is literal", "2 * 3 * 4",
			`[{"type":"text","text":"2 * 3 * 4"}]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.JSONEq(t, tc.want, inlineJSON(t, tc.in))
		})
	}
}

func TestUnitMrkdwnToRichTextBlocks(t *testing.T) {
	t.Run("lists group by style and indent", func(t *testing.T) {
		block := MrkdwnToRichText("• one\n• two\n    ◦ nested\n◦ glyph implies indent")
		require.Len(t, block.Elements, 2)

		top := block.Elements[0].(*slack.RichTextList)
		assert.Equal(t, slack.RTEListBullet, top.Style)
		assert.Equal(t, 0, top.Indent)
		assert.Len(t, top.Elements, 2)

		nested := block.Elements[1].(*slack.RichTextList)
		assert.Equal(t, 1, nested.Indent)
		assert.Len(t, nested.Elements, 2, "whitespace and the ◦ glyph both mean level 1")

		block = MrkdwnToRichText("- dash\n* star\n1. first\n2. second")
		require.Len(t, block.Elements, 2)
		assert.Len(t, block.Elements[0].(*slack.RichTextList).Elements, 2, "- and * are bullets")
		ordered := block.Elements[1].(*slack.RichTextList)
		assert.Equal(t, slack.RTEListOrdered, ordered.Style)
		assert.Equal(t, 0, ordered.Offset)
	})

	t.Run("ordered list keeps its start number", func(t *testing.T) {
		block := MrkdwnToRichText("3. third\n4) fourth")
		require.Len(t, block.Elements, 1)
		list := block.Elements[0].(*slack.RichTextList)
		assert.Equal(t, 2, list.Offset)
		assert.Len(t, list.Elements, 2)
	})

	t.Run("bold line start is not a bullet", func(t *testing.T) {
		block := MrkdwnToRichText("*Heading* text")
		require.Len(t, block.Elements, 1)
		assert.IsType(t, &slack.RichTextSection{}, block.Elements[0])
	})

	t.Run("quotes merge consecutive lines", func(t *testing.T) {
		block := MrkdwnToRichText("> first\n&gt; second\nafter")
		require.Len(t, block.Elements, 2)
		quote := block.Elements[0].(*slack.RichTextQuote)
		raw, err := json.Marshal(quote)
		require.NoError(t, err)
		assert.JSONEq(t, `{"type":"rich_text_quote","elements":[{"type":"text","text":"first\nsecond"}]}`, string(raw))
	})

	t.Run("code fences", func(t *testing.T) {
		for in, want := range map[string]string{
			"```\nline 1\n*line 2*\n```": "line 1\n*line 2*",
			"```inline```":               "inline",
			"```\nunclosed &amp; open":   "unclosed & open",
		} {
			block := MrkdwnToRichText(in)
			require.Len(t, block.Elements, 1, "input %q", in)
			pre := block.Elements[0].(*slack.RichTextPreformatted)
			assert.Equal(t, want, plainText(pre.Elements), "input %q", in)
		}
	})

	t.Run("blank lines between blocks become newline sections, never empty ones", func(t *testing.T) {
		block := MrkdwnToRichText("• item\n\n> quote")
		require.Len(t, block.Elements, 3)
		raw, err := json.Marshal(block.Elements[1])
		require.NoError(t, err)
		assert.JSONEq(t, `{"type":"rich_text_section","elements":[{"type":"text","text":"\n"}]}`, string(raw))
	})

	t.Run("empty bullets and quotes never produce empty elements", func(t *testing.T) {
		for _, in := range []string{"- ", "•", "text\n- \nmore", "text\n>\nmore"} {
			block := MrkdwnToRichText(in)
			require.NotEmpty(t, block.Elements, "input %q", in)
			raw, err := json.Marshal(block.Elements)
			require.NoError(t, err)
			assert.NotContains(t, string(raw), `"elements":null`, "input %q", in)
		}
		for _, in := range []string{">", "> \n>"} {
			assert.Empty(t, MrkdwnToRichText(in).Elements, "input %q has no content", in)
		}
	})

	t.Run("paragraph keeps its line breaks; CRLF and trailing newlines are normalised", func(t *testing.T) {
		assert.JSONEq(t, `[{"type":"text","text":"a\n\nb"}]`, inlineJSON(t, "a\r\n\r\nb\n\n"))
	})
}

func TestUnitRichTextRoundTrip(t *testing.T) {
	inputs := []string{
		"plain line",
		"*Weekly update* :tada:\n\nHi <!subteam^S0TEST5>, thanks <@U0TEST1>!",
		"Intro line:\n• :rocket: *ABC-1* shipped (<https://example.com/a|details>)\n• _progress:_ ABC-2\n    ◦ sub item\n    ◦ another one\n\nClosing line :rainbow:",
		"1. one\n2. two\n\n> a quote\n> continued\n\n```\nsome code\n```\nafter",
		"a &amp; b",
		":trophy: *Week 1 (<https://example.com/a#b|see also \"Notes\">)* :tada:",
		"_italic <@U0TEST1> :wave: and more_",
	}
	for _, in := range inputs {
		out := RichTextToMrkdwn([]slack.Block{MrkdwnToRichText(in)})
		assert.Equal(t, in, out)
	}
}

func TestUnitRichTextToMrkdwnSkipsOtherBlocks(t *testing.T) {
	blocks := []slack.Block{
		slack.NewDividerBlock(),
		MrkdwnToRichText("kept"),
	}
	assert.Equal(t, "kept", RichTextToMrkdwn(blocks))
}

func TestUnitRichTextRoundTripThroughJSON(t *testing.T) {
	// Drafts come back from Slack as JSON, so the renderer must work on
	// blocks decoded by slack-go, not only on ones built in memory.
	in := "• *one* <@U0TEST1>\n    ◦ two :wave::skin-tone-2:"
	raw, err := json.Marshal([]slack.Block{MrkdwnToRichText(in)})
	require.NoError(t, err)

	var decoded slack.Blocks
	require.NoError(t, json.Unmarshal(raw, &decoded))
	assert.Equal(t, in, RichTextToMrkdwn(decoded.BlockSet))
}
