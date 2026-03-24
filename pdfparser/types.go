package pdfparser

import "fmt"

// PDFObject represents any parsed PDF value.
type PDFObject interface {
	Type() string
}

// PDFNull represents the null object.
type PDFNull struct{}

func (PDFNull) Type() string { return "null" }

// PDFBoolean represents a boolean object.
type PDFBoolean struct {
	Value bool
}

func (PDFBoolean) Type() string { return "boolean" }

// PDFNumeric represents numeric objects (integer or real).
type PDFNumeric struct {
	Value float64
	IsInt bool
}

func (PDFNumeric) Type() string { return "numeric" }

// Int64 returns the integer value when IsInt is true.
func (n PDFNumeric) Int64() (int64, error) {
	if !n.IsInt {
		return 0, fmt.Errorf("numeric is not an integer")
	}
	return int64(n.Value), nil
}

// PDFName represents a /Name object.
type PDFName struct {
	Value string
}

func (PDFName) Type() string { return "name" }

// PDFString represents either a literal string (...) or a hex string <...>.
type PDFString struct {
	Value string
	Hex   bool
}

func (PDFString) Type() string { return "string" }

// PDFArray represents an array object.
type PDFArray struct {
	Elements []PDFObject
}

func (PDFArray) Type() string { return "array" }

// PDFDictionary represents a dictionary object.
type PDFDictionary struct {
	Entries map[string]PDFObject
}

func (PDFDictionary) Type() string { return "dictionary" }

// Get returns a dictionary entry by key without leading slash.
func (d PDFDictionary) Get(key string) (PDFObject, bool) {
	v, ok := d.Entries[key]
	return v, ok
}

// PDFStream represents a stream object.
type PDFStream struct {
	Dict PDFDictionary
	Data []byte
}

func (PDFStream) Type() string { return "stream" }

// PDFIndirectRef represents an indirect reference (e.g. 5 0 R).
type PDFIndirectRef struct {
	ObjectNumber     int
	GenerationNumber int
}

func (PDFIndirectRef) Type() string { return "indirect_ref" }

// ObjectID uniquely identifies an indirect object.
type ObjectID struct {
	Number     int
	Generation int
}

// IndirectObject represents one parsed indirect object.
type IndirectObject struct {
	ID    ObjectID
	Value PDFObject
}

// Document is the parsed PDF container.
type Document struct {
	Version string
	Objects map[ObjectID]IndirectObject
	Trailer PDFDictionary
}
