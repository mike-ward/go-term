package term

import (
	"bytes"
	"testing"
)

func TestParser_DA1_Reply(t *testing.T) {
	g, p := newParserGrid(1, 5)
	var replies [][]byte
	p.SetReplyHandler(func(b []byte) {
		replies = append(replies, append([]byte(nil), b...))
	})
	feed(t, g, p, []byte("\x1b[c"))
	if len(replies) != 1 || !bytes.Equal(replies[0], []byte("\x1b[?1;2c")) {
		t.Errorf("DA1 reply: %q", replies)
	}
}

func TestParser_DA1_ExplicitZero(t *testing.T) {
	g, p := newParserGrid(1, 5)
	got := 0
	p.SetReplyHandler(func([]byte) { got++ })
	feed(t, g, p, []byte("\x1b[0c"))
	if got != 1 {
		t.Errorf("CSI 0 c reply count=%d", got)
	}
}

func TestParser_DA1_NonZeroIgnored(t *testing.T) {
	g, p := newParserGrid(1, 5)
	got := 0
	p.SetReplyHandler(func([]byte) { got++ })
	feed(t, g, p, []byte("\x1b[1c"))
	if got != 0 {
		t.Errorf("CSI 1 c should not reply: %d", got)
	}
}

func TestParser_DA1_PrivateIgnored(t *testing.T) {
	g, p := newParserGrid(1, 5)
	got := 0
	p.SetReplyHandler(func([]byte) { got++ })
	feed(t, g, p, []byte("\x1b[?c"))
	if got != 0 {
		t.Errorf("CSI ? c should not reply: %d", got)
	}
}

func TestParser_CPRReply(t *testing.T) {
	g, p := newParserGrid(4, 8)
	g.CursorR, g.CursorC = 2, 5
	var replies [][]byte
	p.SetReplyHandler(func(b []byte) {
		replies = append(replies, append([]byte(nil), b...))
	})
	feed(t, g, p, []byte("\x1b[6n"))
	if len(replies) != 1 || string(replies[0]) != "\x1b[3;6R" {
		t.Fatalf("CPR reply = %q", replies)
	}
}

func TestParser_DCS_UnknownSwallowed(t *testing.T) {
	g, p := newParserGrid(1, 5)
	feed(t, g, p, []byte("\x1bPignored\x1b\\X"))
	if got := g.At(0, 0).Ch; got != 'X' {
		t.Fatalf("DCS leaked into grid: got %q want X", got)
	}
}

func TestParser_XTGETTCAP_Reply(t *testing.T) {
	g, p := newParserGrid(1, 5)
	var replies []string
	p.SetReplyHandler(func(b []byte) { replies = append(replies, string(b)) })
	feed(t, g, p, []byte("\x1bP+q544e;6b63757531\x1b\\"))
	want := "\x1bP1+r544e=787465726d2d323536636f6c6f72;6b63757531=1b5b41\x1b\\"
	if len(replies) != 1 || replies[0] != want {
		t.Fatalf("XTGETTCAP = %q, want %q", replies, want)
	}
}

func TestParser_XTGETTCAP_UnknownCapReturnsHexName(t *testing.T) {

	g, p := newParserGrid(1, 5)
	var replies []string
	p.SetReplyHandler(func(b []byte) { replies = append(replies, string(b)) })
	feed(t, g, p, []byte("\x1bP+q756e6b6e6f776e\x1b\\"))
	want := "\x1bP0+r756e6b6e6f776e\x1b\\"
	if len(replies) != 1 || replies[0] != want {
		t.Fatalf("unknown cap reply = %q, want %q", replies, want)
	}
}

func TestParser_XTGETTCAP_InvalidHexReturnsErrorWithPart(t *testing.T) {

	g, p := newParserGrid(1, 5)
	var replies []string
	p.SetReplyHandler(func(b []byte) { replies = append(replies, string(b)) })
	feed(t, g, p, []byte("\x1bP+q54e\x1b\\"))
	want := "\x1bP0+r54e\x1b\\"
	if len(replies) != 1 || replies[0] != want {
		t.Fatalf("invalid hex reply = %q, want %q", replies, want)
	}
}

func TestParser_XTGETTCAP_EmptyBodyReturnsError(t *testing.T) {
	g, p := newParserGrid(1, 5)
	var replies []string
	p.SetReplyHandler(func(b []byte) { replies = append(replies, string(b)) })
	feed(t, g, p, []byte("\x1bP+q\x1b\\"))
	want := "\x1bP0+r\x1b\\"
	if len(replies) != 1 || replies[0] != want {
		t.Fatalf("empty body reply = %q, want %q", replies, want)
	}
}

func TestSplitSemis_CapsAtMax(t *testing.T) {
	// 41 fields separated by 40 semicolons — must be capped at maxXTGETTCAPParts
	var b []byte
	for i := 0; i < 41; i++ {
		if i > 0 {
			b = append(b, ';')
		}
		b = append(b, 'x')
	}
	parts := splitSemis(b)
	if len(parts) > maxXTGETTCAPParts {
		t.Errorf("splitSemis returned %d parts, want ≤%d", len(parts), maxXTGETTCAPParts)
	}
}
