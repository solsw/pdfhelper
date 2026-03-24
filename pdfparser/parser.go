package pdfparser

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

// Parse parses a PDF file with a classic cross-reference table.
func Parse(data []byte) (*Document, error) {
	version, err := parseVersion(data)
	if err != nil {
		return nil, err
	}

	xrefOffset, err := findStartXRef(data)
	if err != nil {
		return nil, err
	}

	entries, trailer, err := parseXRefTable(data, xrefOffset)
	if err != nil {
		return nil, err
	}

	doc := &Document{
		Version: version,
		Objects: make(map[ObjectID]IndirectObject),
		Trailer: trailer,
	}

	for id, offset := range entries {
		if offset < 0 || offset >= len(data) {
			continue
		}
		obj, err := parseIndirectObjectAt(data, offset)
		if err != nil {
			return nil, fmt.Errorf("parse object %d %d at %d: %w", id.Number, id.Generation, offset, err)
		}
		doc.Objects[id] = obj
	}

	return doc, nil
}

func parseVersion(data []byte) (string, error) {
	idx := bytes.Index(data, []byte("%PDF-"))
	if idx < 0 || idx+8 > len(data) {
		return "", fmt.Errorf("missing PDF header")
	}
	lineEnd := bytes.IndexAny(data[idx:], "\r\n")
	if lineEnd < 0 {
		return "", fmt.Errorf("invalid PDF header")
	}
	line := string(data[idx : idx+lineEnd])
	if !strings.HasPrefix(line, "%PDF-") {
		return "", fmt.Errorf("invalid PDF signature")
	}
	return strings.TrimPrefix(line, "%PDF-"), nil
}

func findStartXRef(data []byte) (int, error) {
	idx := bytes.LastIndex(data, []byte("startxref"))
	if idx < 0 {
		return 0, fmt.Errorf("startxref not found")
	}
	pos := idx + len("startxref")
	pos = skipWS(data, pos)
	num, next, err := readInteger(data, pos)
	if err != nil {
		return 0, fmt.Errorf("invalid startxref value: %w", err)
	}
	_ = next
	return num, nil
}

func parseXRefTable(data []byte, offset int) (map[ObjectID]int, PDFDictionary, error) {
	pos := skipWS(data, offset)
	if !hasPrefixAt(data, pos, "xref") {
		return nil, PDFDictionary{}, fmt.Errorf("xref keyword not found at offset %d", offset)
	}
	pos += len("xref")
	entries := map[ObjectID]int{}

	for {
		pos = skipWSAndComments(data, pos)
		if hasPrefixAt(data, pos, "trailer") {
			pos += len("trailer")
			break
		}

		startObj, next, err := readInteger(data, pos)
		if err != nil {
			return nil, PDFDictionary{}, fmt.Errorf("invalid xref subsection start: %w", err)
		}
		count, next2, err := readInteger(data, skipWS(data, next))
		if err != nil {
			return nil, PDFDictionary{}, fmt.Errorf("invalid xref subsection count: %w", err)
		}
		pos = next2

		for i := 0; i < count; i++ {
			pos = skipWS(data, pos)
			offStr, n1, err := readDigits(data, pos, 10)
			if err != nil {
				return nil, PDFDictionary{}, fmt.Errorf("invalid xref offset: %w", err)
			}
			genStr, n2, err := readDigits(data, skipWS(data, n1), 5)
			if err != nil {
				return nil, PDFDictionary{}, fmt.Errorf("invalid xref generation: %w", err)
			}
			n2 = skipWS(data, n2)
			if n2 >= len(data) {
				return nil, PDFDictionary{}, fmt.Errorf("unexpected EOF in xref entry")
			}
			status := data[n2]
			pos = n2 + 1
			offsetVal, _ := strconv.Atoi(offStr)
			genVal, _ := strconv.Atoi(genStr)
			if status == 'n' {
				entries[ObjectID{Number: startObj + i, Generation: genVal}] = offsetVal
			}
		}
	}

	p := &reader{data: data, pos: skipWSAndComments(data, pos)}
	dict, err := p.parseDictionary()
	if err != nil {
		return nil, PDFDictionary{}, fmt.Errorf("invalid trailer dictionary: %w", err)
	}
	return entries, dict, nil
}

func parseIndirectObjectAt(data []byte, offset int) (IndirectObject, error) {
	p := &reader{data: data, pos: skipWSAndComments(data, offset)}
	num, err := p.readInt()
	if err != nil {
		return IndirectObject{}, err
	}
	gen, err := p.readInt()
	if err != nil {
		return IndirectObject{}, err
	}
	if err := p.expectKeyword("obj"); err != nil {
		return IndirectObject{}, err
	}

	val, err := p.parseObject()
	if err != nil {
		return IndirectObject{}, err
	}

	// stream object is dictionary + stream keyword
	if dict, ok := val.(PDFDictionary); ok {
		saved := p.pos
		p.skipWSAndComments()
		if p.hasPrefix("stream") {
			p.pos += len("stream")
			if p.pos < len(p.data) && p.data[p.pos] == '\r' {
				p.pos++
			}
			if p.pos < len(p.data) && p.data[p.pos] == '\n' {
				p.pos++
			}
			streamData, err := p.readStreamData(dict)
			if err != nil {
				return IndirectObject{}, err
			}
			val = PDFStream{Dict: dict, Data: streamData}
		} else {
			p.pos = saved
		}
	}

	if err := p.expectKeyword("endobj"); err != nil {
		return IndirectObject{}, err
	}

	return IndirectObject{ID: ObjectID{Number: num, Generation: gen}, Value: val}, nil
}

type reader struct {
	data []byte
	pos  int
}

func (r *reader) parseObject() (PDFObject, error) {
	r.skipWSAndComments()
	if r.pos >= len(r.data) {
		return nil, fmt.Errorf("unexpected EOF")
	}

	if r.hasPrefix("null") {
		r.pos += 4
		return PDFNull{}, nil
	}
	if r.hasPrefix("true") {
		r.pos += 4
		return PDFBoolean{Value: true}, nil
	}
	if r.hasPrefix("false") {
		r.pos += 5
		return PDFBoolean{Value: false}, nil
	}
	if r.data[r.pos] == '/' {
		name, err := r.parseName()
		if err != nil {
			return nil, err
		}
		return PDFName{Value: name}, nil
	}
	if r.data[r.pos] == '(' {
		s, err := r.parseLiteralString()
		if err != nil {
			return nil, err
		}
		return PDFString{Value: s}, nil
	}
	if r.data[r.pos] == '[' {
		return r.parseArray()
	}
	if r.hasPrefix("<<") {
		return r.parseDictionary()
	}
	if r.data[r.pos] == '<' {
		h, err := r.parseHexString()
		if err != nil {
			return nil, err
		}
		return PDFString{Value: h, Hex: true}, nil
	}
	if isNumberStart(r.data[r.pos]) {
		return r.parseNumberOrRef()
	}

	return nil, fmt.Errorf("unknown token at %d", r.pos)
}

func (r *reader) parseNumberOrRef() (PDFObject, error) {
	start := r.pos
	first, err := r.parseNumber()
	if err != nil {
		return nil, err
	}
	r.skipWSAndComments()

	// Indirect reference pattern: int int R
	if n1, ok1 := first.(PDFNumeric); ok1 && n1.IsInt {
		save := r.pos
		if r.pos < len(r.data) && isNumberStart(r.data[r.pos]) {
			second, err := r.parseNumber()
			if err == nil {
				r.skipWSAndComments()
				if n2, ok2 := second.(PDFNumeric); ok2 && ok1 && n2.IsInt && r.pos < len(r.data) && r.data[r.pos] == 'R' {
					r.pos++
					return PDFIndirectRef{ObjectNumber: int(n1.Value), GenerationNumber: int(n2.Value)}, nil
				}
			}
			r.pos = save
		}
	}

	if r.pos == start {
		return nil, fmt.Errorf("invalid number")
	}
	return first, nil
}

func (r *reader) parseNumber() (PDFObject, error) {
	start := r.pos
	if r.pos < len(r.data) && (r.data[r.pos] == '+' || r.data[r.pos] == '-') {
		r.pos++
	}
	hasDot := false
	for r.pos < len(r.data) {
		c := r.data[r.pos]
		if c == '.' {
			if hasDot {
				break
			}
			hasDot = true
			r.pos++
			continue
		}
		if c < '0' || c > '9' {
			break
		}
		r.pos++
	}
	if r.pos == start || (r.pos == start+1 && (r.data[start] == '+' || r.data[start] == '-')) {
		return nil, fmt.Errorf("invalid number")
	}
	vStr := string(r.data[start:r.pos])
	v, err := strconv.ParseFloat(vStr, 64)
	if err != nil {
		return nil, err
	}
	return PDFNumeric{Value: v, IsInt: !hasDot}, nil
}

func (r *reader) parseName() (string, error) {
	if r.data[r.pos] != '/' {
		return "", fmt.Errorf("expected name")
	}
	r.pos++
	start := r.pos
	for r.pos < len(r.data) && !isDelimiter(r.data[r.pos]) && !isWhite(r.data[r.pos]) {
		r.pos++
	}
	raw := string(r.data[start:r.pos])
	return decodeName(raw), nil
}

func (r *reader) parseLiteralString() (string, error) {
	if r.data[r.pos] != '(' {
		return "", fmt.Errorf("expected literal string")
	}
	r.pos++
	depth := 1
	var out []byte
	for r.pos < len(r.data) {
		c := r.data[r.pos]
		r.pos++
		if c == '\\' {
			if r.pos >= len(r.data) {
				break
			}
			n := r.data[r.pos]
			r.pos++
			switch n {
			case 'n':
				out = append(out, '\n')
			case 'r':
				out = append(out, '\r')
			case 't':
				out = append(out, '\t')
			case 'b':
				out = append(out, '\b')
			case 'f':
				out = append(out, '\f')
			default:
				out = append(out, n)
			}
			continue
		}
		if c == '(' {
			depth++
		} else if c == ')' {
			depth--
			if depth == 0 {
				return string(out), nil
			}
		}
		out = append(out, c)
	}
	return "", fmt.Errorf("unterminated literal string")
}

func (r *reader) parseHexString() (string, error) {
	if r.data[r.pos] != '<' || r.hasPrefix("<<") {
		return "", fmt.Errorf("expected hex string")
	}
	r.pos++
	start := r.pos
	for r.pos < len(r.data) && r.data[r.pos] != '>' {
		r.pos++
	}
	if r.pos >= len(r.data) {
		return "", fmt.Errorf("unterminated hex string")
	}
	hexStr := string(bytes.Map(func(r rune) rune {
		if r == ' ' || r == '\n' || r == '\r' || r == '\t' || r == '\f' || r == '\v' {
			return -1
		}
		return r
	}, r.data[start:r.pos]))
	r.pos++
	if len(hexStr)%2 == 1 {
		hexStr += "0"
	}
	decoded, err := hex.DecodeString(hexStr)
	if err != nil {
		return "", err
	}
	return string(decoded), nil
}

func (r *reader) parseArray() (PDFObject, error) {
	if r.data[r.pos] != '[' {
		return nil, fmt.Errorf("expected array")
	}
	r.pos++
	arr := PDFArray{}
	for {
		r.skipWSAndComments()
		if r.pos >= len(r.data) {
			return nil, fmt.Errorf("unterminated array")
		}
		if r.data[r.pos] == ']' {
			r.pos++
			return arr, nil
		}
		obj, err := r.parseObject()
		if err != nil {
			return nil, err
		}
		arr.Elements = append(arr.Elements, obj)
	}
}

func (r *reader) parseDictionary() (PDFDictionary, error) {
	if !r.hasPrefix("<<") {
		return PDFDictionary{}, fmt.Errorf("expected dictionary")
	}
	r.pos += 2
	dict := PDFDictionary{Entries: map[string]PDFObject{}}
	for {
		r.skipWSAndComments()
		if r.hasPrefix(">>") {
			r.pos += 2
			return dict, nil
		}
		key, err := r.parseName()
		if err != nil {
			return PDFDictionary{}, err
		}
		value, err := r.parseObject()
		if err != nil {
			return PDFDictionary{}, err
		}
		dict.Entries[key] = value
	}
}

func (r *reader) readStreamData(dict PDFDictionary) ([]byte, error) {
	if obj, ok := dict.Get("Length"); ok {
		if num, ok := obj.(PDFNumeric); ok && num.IsInt {
			length := int(num.Value)
			if length < 0 || r.pos+length > len(r.data) {
				return nil, fmt.Errorf("invalid stream length")
			}
			data := r.data[r.pos : r.pos+length]
			r.pos += length
			r.skipWSAndComments()
			if !r.hasPrefix("endstream") {
				return nil, fmt.Errorf("missing endstream")
			}
			r.pos += len("endstream")
			return data, nil
		}
	}

	end := bytes.Index(r.data[r.pos:], []byte("endstream"))
	if end < 0 {
		return nil, fmt.Errorf("missing endstream")
	}
	data := r.data[r.pos : r.pos+end]
	r.pos += end + len("endstream")
	return data, nil
}

func (r *reader) readInt() (int, error) {
	r.skipWSAndComments()
	n, next, err := readInteger(r.data, r.pos)
	if err != nil {
		return 0, err
	}
	r.pos = next
	return n, nil
}

func (r *reader) expectKeyword(kw string) error {
	r.skipWSAndComments()
	if !r.hasPrefix(kw) {
		return fmt.Errorf("expected %q", kw)
	}
	r.pos += len(kw)
	return nil
}

func (r *reader) hasPrefix(s string) bool {
	return hasPrefixAt(r.data, r.pos, s)
}

func (r *reader) skipWSAndComments() {
	r.pos = skipWSAndComments(r.data, r.pos)
}

func skipWSAndComments(data []byte, pos int) int {
	for {
		pos = skipWS(data, pos)
		if pos < len(data) && data[pos] == '%' {
			for pos < len(data) && data[pos] != '\n' && data[pos] != '\r' {
				pos++
			}
			continue
		}
		return pos
	}
}

func skipWS(data []byte, pos int) int {
	for pos < len(data) && isWhite(data[pos]) {
		pos++
	}
	return pos
}

func isWhite(c byte) bool {
	switch c {
	case 0, 9, 10, 12, 13, 32:
		return true
	default:
		return false
	}
}

func isDelimiter(c byte) bool {
	switch c {
	case '(', ')', '<', '>', '[', ']', '{', '}', '/', '%':
		return true
	default:
		return false
	}
}

func hasPrefixAt(data []byte, pos int, s string) bool {
	if pos+len(s) > len(data) {
		return false
	}
	return string(data[pos:pos+len(s)]) == s
}

func isNumberStart(c byte) bool {
	return (c >= '0' && c <= '9') || c == '+' || c == '-' || c == '.'
}

func readInteger(data []byte, pos int) (int, int, error) {
	start := pos
	if pos < len(data) && (data[pos] == '+' || data[pos] == '-') {
		pos++
	}
	for pos < len(data) && data[pos] >= '0' && data[pos] <= '9' {
		pos++
	}
	if pos == start || (pos == start+1 && (data[start] == '+' || data[start] == '-')) {
		return 0, start, fmt.Errorf("expected integer")
	}
	n, err := strconv.Atoi(string(data[start:pos]))
	if err != nil {
		return 0, start, err
	}
	return n, pos, nil
}

func readDigits(data []byte, pos int, min int) (string, int, error) {
	start := pos
	for pos < len(data) && data[pos] >= '0' && data[pos] <= '9' {
		pos++
	}
	if pos-start < min {
		return "", start, fmt.Errorf("expected at least %d digits", min)
	}
	return string(data[start:pos]), pos, nil
}

func decodeName(raw string) string {
	var out []byte
	for i := 0; i < len(raw); i++ {
		if raw[i] == '#' && i+2 < len(raw) {
			v, err := strconv.ParseInt(raw[i+1:i+3], 16, 32)
			if err == nil {
				out = append(out, byte(v))
				i += 2
				continue
			}
		}
		out = append(out, raw[i])
	}
	return string(out)
}
