package main

// Camera thường xuất G.711U (ulaw) trong khi track audio phía client luôn là
// PCMA (alaw) — track được tạo trước negotiation nên không đổi codec được nữa.
// Chuyển từng mẫu qua bảng 256 byte (dựng lúc init) thay vì transcode.
var alawFromUlaw = func() (table [256]byte) {
	for i := range table {
		table[i] = linearToAlaw(ulawToLinear(byte(i)))
	}
	return
}()

// segAEnd: ngưỡng segment của A-law (G.711 reference implementation).
var segAEnd = [8]int32{0x1F, 0x3F, 0x7F, 0xFF, 0x1FF, 0x3FF, 0x7FF, 0xFFF}

func ulawToLinear(sample byte) int16 {
	value := ^sample
	linear := int32(value&0x0F)<<3 + 0x84
	linear <<= (value & 0x70) >> 4
	if value&0x80 != 0 {
		return int16(0x84 - linear)
	}
	return int16(linear - 0x84)
}

func linearToAlaw(pcm int16) byte {
	sample := int32(pcm) >> 3
	mask := int32(0xD5) // mẫu dương: bit dấu = 1
	if sample < 0 {
		mask = 0x55
		sample = -sample - 1
	}
	segment := 0
	for segment < 8 && sample > segAEnd[segment] {
		segment++
	}
	if segment >= 8 {
		return byte(0x7F ^ mask)
	}
	value := int32(segment) << 4
	if segment < 2 {
		value |= (sample >> 1) & 0x0F
	} else {
		value |= (sample >> segment) & 0x0F
	}
	return byte(value ^ mask)
}
