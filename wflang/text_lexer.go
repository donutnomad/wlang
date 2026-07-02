package wflang

import (
	"bytes"
	"unicode"
	"unicode/utf8"
)

type textTokenKind int

const (
	textEOF textTokenKind = iota
	textNewline
	textIdent
	textNumber
	textString
	textSymbol
)

type textToken struct {
	kind textTokenKind
	lit  string
	line int
	col  int
}

type textLexer struct {
	src       []byte
	pos       int
	line      int
	col       int
	peeked    textToken
	hasPeeked bool
}

func newTextLexer(src []byte) *textLexer {
	return &textLexer{src: src, line: 1, col: 1}
}

func (l *textLexer) peek() textToken {
	if !l.hasPeeked {
		l.peeked = l.next()
		l.hasPeeked = true
	}
	return l.peeked
}

func (l *textLexer) take() textToken {
	t := l.peek()
	l.hasPeeked = false
	return t
}

func (l *textLexer) next() textToken {
	for l.pos < len(l.src) {
		r, n := utf8.DecodeRune(l.src[l.pos:])
		if r == ' ' || r == '\t' || r == '\r' {
			l.advance(n, r)
			continue
		}
		if r == '#' {
			for l.pos < len(l.src) {
				rr, nn := utf8.DecodeRune(l.src[l.pos:])
				if rr == '\n' {
					break
				}
				l.advance(nn, rr)
			}
			continue
		}
		break
	}
	if l.pos >= len(l.src) {
		return textToken{kind: textEOF, line: l.line, col: l.col}
	}
	line, col := l.line, l.col
	r, n := utf8.DecodeRune(l.src[l.pos:])
	if r == '\n' {
		l.advance(n, r)
		return textToken{kind: textNewline, lit: "\n", line: line, col: col}
	}
	if r == '"' {
		return l.stringToken()
	}
	if unicode.IsDigit(r) {
		return l.numberToken()
	}
	if isIdentStart(r) {
		return l.identToken()
	}
	for _, op := range []string{":=", "->", "<-", "??", ">=", "<=", "==", "!="} {
		if bytes.HasPrefix(l.src[l.pos:], []byte(op)) {
			for _, rr := range op {
				l.advance(utf8.RuneLen(rr), rr)
			}
			return textToken{kind: textSymbol, lit: op, line: line, col: col}
		}
	}
	l.advance(n, r)
	return textToken{kind: textSymbol, lit: string(r), line: line, col: col}
}

func (l *textLexer) advance(n int, r rune) {
	l.pos += n
	if r == '\n' {
		l.line++
		l.col = 1
		return
	}
	l.col++
}

func (l *textLexer) identToken() textToken {
	line, col := l.line, l.col
	start := l.pos
	for l.pos < len(l.src) {
		r, n := utf8.DecodeRune(l.src[l.pos:])
		if !isIdentPart(r) {
			break
		}
		l.advance(n, r)
	}
	return textToken{kind: textIdent, lit: string(l.src[start:l.pos]), line: line, col: col}
}

func (l *textLexer) numberToken() textToken {
	line, col := l.line, l.col
	start := l.pos
	seenDot := false
	for l.pos < len(l.src) {
		r, n := utf8.DecodeRune(l.src[l.pos:])
		if unicode.IsDigit(r) {
			l.advance(n, r)
			continue
		}
		if r == '.' && !seenDot {
			seenDot = true
			l.advance(n, r)
			continue
		}
		break
	}
	return textToken{kind: textNumber, lit: string(l.src[start:l.pos]), line: line, col: col}
}

func (l *textLexer) stringToken() textToken {
	line, col := l.line, l.col
	start := l.pos
	l.advance(1, '"')
	escaped := false
	for l.pos < len(l.src) {
		r, n := utf8.DecodeRune(l.src[l.pos:])
		l.advance(n, r)
		if escaped {
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		if r == '"' {
			break
		}
	}
	return textToken{kind: textString, lit: string(l.src[start:l.pos]), line: line, col: col}
}

func isIdentStart(r rune) bool {
	return r == '_' || unicode.IsLetter(r)
}

func isIdentPart(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}
