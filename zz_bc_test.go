package render

import (
	"fmt"
	"testing"
)

func TestBCDump(t *testing.T) {
	b, err := ParseHTML([]byte(`<html><body><NAVBAR/><YIELD/><FOOTER/></body></html>`), "layout")
	if err != nil {
		t.Fatal(err)
	}
	stream := b.Stream()
	bc := CompileBytecode(stream)
	for i, ins := range bc.code {
		op := uint8(ins)
		a, bb := uint32(ins>>8), uint32(ins>>40)
		fmt.Printf("[%d] op=%d a=%d b=%d", i, op, a, bb)
		if op == opComponent || op == opSlotBegin {
			fmt.Printf(" name=%q", bc.comps[a])
		}
		fmt.Println()
	}
	fmt.Printf("static len=%d\n", len(bc.static))
}
