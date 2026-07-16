package typewriter

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
)

const maxAudioFrames = 44100 * 2

func decodeWAVPCM16(b []byte, expectedRate int) ([]byte, error) {
	if len(b) < 12 || string(b[:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		return nil, errors.New("not a RIFF/WAVE file")
	}
	declared := uint64(binary.LittleEndian.Uint32(b[4:8])) + 8
	if declared != uint64(len(b)) {
		return nil, fmt.Errorf("RIFF length is %d, actual length is %d", declared, len(b))
	}
	var haveFmt bool
	var pcm []byte
	for pos := 12; pos < len(b); {
		if len(b)-pos < 8 {
			return nil, errors.New("truncated chunk header")
		}
		name := string(b[pos : pos+4])
		sz := int(binary.LittleEndian.Uint32(b[pos+4 : pos+8]))
		pos += 8
		if sz < 0 || sz > len(b)-pos {
			return nil, fmt.Errorf("chunk %q exceeds file", name)
		}
		chunk := b[pos : pos+sz]
		switch name {
		case "fmt ":
			if haveFmt || len(chunk) < 16 {
				return nil, errors.New("missing or duplicate fmt chunk")
			}
			haveFmt = true
			format := binary.LittleEndian.Uint16(chunk[0:2])
			channels := binary.LittleEndian.Uint16(chunk[2:4])
			rate := binary.LittleEndian.Uint32(chunk[4:8])
			byteRate := binary.LittleEndian.Uint32(chunk[8:12])
			blockAlign := binary.LittleEndian.Uint16(chunk[12:14])
			bits := binary.LittleEndian.Uint16(chunk[14:16])
			if format != 1 || channels != 1 || rate != uint32(expectedRate) || bits != 16 || blockAlign != 2 || byteRate != rate*2 {
				return nil, fmt.Errorf("want mono PCM16 at %d Hz", expectedRate)
			}
		case "data":
			if pcm != nil {
				return nil, errors.New("duplicate data chunk")
			}
			if sz == 0 || sz%2 != 0 || sz/2 > maxAudioFrames {
				return nil, errors.New("audio must contain at least one and at most 88200 complete PCM16 frames")
			}
			pcm = bytes.Clone(chunk)
		}
		pos += sz
		if sz&1 == 1 {
			if pos >= len(b) {
				return nil, errors.New("missing WAV chunk padding")
			}
			pos++
		}
	}
	if !haveFmt || pcm == nil {
		return nil, errors.New("WAV requires fmt and data chunks")
	}
	return pcm, nil
}
