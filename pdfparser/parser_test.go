package pdfparser

import (
	"fmt"
	"strings"
	"testing"
)

func buildSimplePDF() []byte {
	obj1 := "1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n"
	obj2 := "2 0 obj\n<< /Type /Pages /Kids [3 0 R] /Count 1 >>\nendobj\n"
	obj3 := "3 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 300 144] /Contents 4 0 R >>\nendobj\n"
	stream := "BT\n/F1 12 Tf\n72 72 Td\n(Hello) Tj\nET\n"
	obj4 := fmt.Sprintf("4 0 obj\n<< /Length %d >>\nstream\n%sendstream\nendobj\n", len(stream), stream)

	var b strings.Builder
	b.WriteString("%PDF-1.4\n")
	offsets := []int{0}
	for _, obj := range []string{obj1, obj2, obj3, obj4} {
		offsets = append(offsets, b.Len())
		b.WriteString(obj)
	}
	xrefOffset := b.Len()
	b.WriteString("xref\n")
	b.WriteString("0 5\n")
	b.WriteString("0000000000 65535 f \n")
	for i := 1; i <= 4; i++ {
		b.WriteString(fmt.Sprintf("%010d 00000 n \n", offsets[i]))
	}
	b.WriteString("trailer\n")
	b.WriteString("<< /Size 5 /Root 1 0 R >>\n")
	b.WriteString("startxref\n")
	b.WriteString(fmt.Sprintf("%d\n", xrefOffset))
	b.WriteString("%%EOF\n")
	return []byte(b.String())
}

func TestParseClassicXRefPDF(t *testing.T) {
	doc, err := Parse(buildSimplePDF())
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if doc.Version != "1.4" {
		t.Fatalf("unexpected version %q", doc.Version)
	}

	if len(doc.Objects) != 4 {
		t.Fatalf("expected 4 objects, got %d", len(doc.Objects))
	}

	obj4, ok := doc.Objects[ObjectID{Number: 4, Generation: 0}]
	if !ok {
		t.Fatalf("missing object 4 0")
	}
	stream, ok := obj4.Value.(PDFStream)
	if !ok {
		t.Fatalf("object 4 is not a stream")
	}
	if !strings.Contains(string(stream.Data), "Hello") {
		t.Fatalf("unexpected stream data: %q", string(stream.Data))
	}
}

func TestParseBasicObjects(t *testing.T) {
	p := &reader{data: []byte("[null true false 12 -3.5 /A (B) <4344> << /K 1 >>]")}
	obj, err := p.parseObject()
	if err != nil {
		t.Fatalf("parseObject failed: %v", err)
	}
	arr, ok := obj.(PDFArray)
	if !ok {
		t.Fatalf("expected array, got %T", obj)
	}
	if len(arr.Elements) != 9 {
		t.Fatalf("unexpected element count %d", len(arr.Elements))
	}
}
