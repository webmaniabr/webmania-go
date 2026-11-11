package webmania

import (
	"encoding/binary"
	"testing"
	"unicode/utf16"
)

func TestAlpha3XMLBoundaries(t *testing.T) {
	for _, tc := range []struct {
		data  string
		ready bool
	}{
		{`<nfeProc version="4"/>`, true},
		{`<nfeProc version="4" version="5"/>`, false},
		{`<nfeProc xmlns:a="urn:test" xmlns:b="urn:test" a:id="1" b:id="2"/>`, false},
		{`<nfeProc undeclared:id="1"/>`, false},
		{`<nfeProc xmlns:a="urn:test" a:id="1"/>`, true},
		{`<!DOCTYPE nfeProc [<!ENTITY ext SYSTEM "http://invalid.test/private">]><nfeProc/>`, false},
		{string([]byte{'<', 'n', 'f', 'e', 'P', 'r', 'o', 'c', '>', 0xff, '<', '/', 'n', 'f', 'e', 'P', 'r', 'o', 'c', '>'}), false},
	} {
		if got := inspectXML([]byte(tc.data), nil); (got == nil) != tc.ready {
			t.Errorf("XML readiness=%t error=%v", tc.ready, got)
		}
	}
	for _, order := range []binary.ByteOrder{binary.LittleEndian, binary.BigEndian} {
		words := utf16.Encode([]rune(`<?xml version="1.0" encoding="UTF-16"?><nfeProc name="ação fiscal"/>`))
		b := make([]byte, 2+2*len(words))
		order.PutUint16(b, 0xfeff)
		for i, w := range words {
			order.PutUint16(b[2+2*i:], w)
		}
		if e := inspectXML(b, nil); e != nil {
			t.Fatal("valid UTF16 refused", e)
		}
		order.PutUint16(b[len(b)-2:], 0xd800)
		if e := inspectXML(b, nil); e == nil {
			t.Fatal("invalid UTF16 accepted")
		}
	}
}
