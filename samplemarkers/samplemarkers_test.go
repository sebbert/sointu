package samplemarkers

import (
	"bytes"
	"encoding/binary"
	"slices"
	"testing"
)

func chunk(order binary.ByteOrder, id string, body []byte) []byte {
	var b bytes.Buffer
	b.WriteString(id)
	binary.Write(&b, order, uint32(len(body)))
	b.Write(body)
	if len(body)%2 == 1 {
		b.WriteByte(0)
	}
	return b.Bytes()
}

func le(vs ...any) []byte {
	var b bytes.Buffer
	for _, v := range vs {
		binary.Write(&b, binary.LittleEndian, v)
	}
	return b.Bytes()
}

func testWav(rate uint32, cues []uint32, labels map[uint32]string, acid []byte) []byte {
	l := binary.LittleEndian
	body := []byte("WAVE")
	body = append(body, chunk(l, "fmt ", le(uint16(1), uint16(1), rate, rate*2, uint16(2), uint16(16)))...)
	body = append(body, chunk(l, "data", make([]byte, 2000))...)
	cue := le(uint32(len(cues)))
	for i, f := range cues {
		cue = append(cue, le(uint32(i+1), f, []byte("data"), uint32(0), uint32(0), f)...)
	}
	body = append(body, chunk(l, "cue ", cue)...)
	adtl := []byte("adtl")
	for id, name := range labels {
		adtl = append(adtl, chunk(l, "labl", append(le(id), append([]byte(name), 0)...))...)
	}
	body = append(body, chunk(l, "LIST", adtl)...)
	if acid != nil {
		body = append(body, chunk(l, "acid", acid)...)
	}
	return chunk(l, "RIFF", body)
}

func TestWavCuePoints(t *testing.T) {
	info, ok := Read(testWav(48000, []uint32{0, 9600, 4800}, map[uint32]string{2: "snare"}, le(uint32(0), uint16(60), uint16(0), float32(0), uint32(8), uint16(4), uint16(4), float32(120))))
	if !ok || info.Rate != 48000 || len(info.Markers) != 3 || info.Markers[1].Name != "snare" || info.Beats != 8 {
		t.Fatalf("got %+v, %v", info, ok)
	}
	// at 44100 Hz, without the cue at 0
	if got := info.Slices(); !slices.Equal(got, []int{4410, 8820}) {
		t.Errorf("slices %v", got)
	}
	// a one shot has no beats
	info, _ = Read(testWav(44100, nil, nil, le(uint32(1), uint16(60), uint16(0), float32(0), uint32(8))))
	if info.Beats != 0 || info.Slices() != nil {
		t.Errorf("a one shot: %+v", info)
	}
}

func TestAiffMarkers(t *testing.T) {
	b := binary.BigEndian
	var comm bytes.Buffer
	binary.Write(&comm, b, uint16(1))
	binary.Write(&comm, b, uint32(1000))
	binary.Write(&comm, b, uint16(16))
	comm.Write([]byte{0x40, 0x0d, 0xac, 0x44, 0, 0, 0, 0, 0, 0}) // 22050
	var mark bytes.Buffer
	binary.Write(&mark, b, uint16(2))
	for i, m := range []struct {
		frame uint32
		name  string
	}{{500, "b"}, {250, "hat"}} {
		binary.Write(&mark, b, uint16(i+1))
		binary.Write(&mark, b, m.frame)
		mark.WriteByte(byte(len(m.name)))
		mark.WriteString(m.name)
		if (len(m.name)+1)%2 == 1 {
			mark.WriteByte(0)
		}
	}
	body := append([]byte("AIFF"), chunk(b, "COMM", comm.Bytes())...)
	body = append(body, chunk(b, "MARK", mark.Bytes())...)
	body = append(body, chunk(b, "SSND", make([]byte, 2008))...)
	info, ok := Read(chunk(b, "FORM", body))
	if !ok || info.Rate != 22050 || len(info.Markers) != 2 || info.Markers[1].Name != "hat" {
		t.Fatalf("got %+v, %v", info, ok)
	}
	if got := info.Slices(); !slices.Equal(got, []int{500, 1000}) {
		t.Errorf("slices %v", got)
	}
}

func TestNotAudio(t *testing.T) {
	if _, ok := Read([]byte("fLaC....")); ok {
		t.Error("read a FLAC file")
	}
	if _, ok := Read(testWav(44100, nil, nil, nil)[:30]); ok {
		t.Error("read a truncated file")
	}
}
