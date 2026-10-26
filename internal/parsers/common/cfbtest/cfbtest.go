package cfbtest

import (
	"bytes"
)

// cfbSignature starts an OLE compound file.
var cfbSignature = []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}

// CFBFile is an OLE compound file that names one stream.
func CFBFile(stream string) []byte {
	var name bytes.Buffer
	for _, r := range stream {
		name.WriteByte(byte(r))
		name.WriteByte(0)
	}
	return append(append(append([]byte{}, cfbSignature...), make([]byte, 504)...), name.Bytes()...)
}
