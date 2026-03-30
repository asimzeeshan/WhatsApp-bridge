package media

import (
	"encoding/binary"
	"errors"
	"math"
)

// AnalyzeOggOpus parses an OGG Opus stream and returns:
//   - duration in seconds
//   - a 64-sample waveform (each byte in [0,100]) for WhatsApp PTT display
func AnalyzeOggOpus(data []byte) (uint32, []byte, error) {
	if len(data) < 4 {
		return 0, nil, errors.New("data too short for OGG")
	}

	var (
		preSkip     uint16
		lastGranule uint64
		loudness    []float64
		offset      int
		foundHead   bool
	)

	for offset < len(data) {
		page, granule, payload, newOffset, err := readOggPage(data, offset)
		if err != nil {
			break
		}
		_ = page

		// Parse OpusHead from the first data page
		if !foundHead && len(payload) >= 19 && string(payload[:8]) == "OpusHead" {
			preSkip = binary.LittleEndian.Uint16(payload[10:12])
			foundHead = true
		}

		if granule > 0 && granule != 0xFFFFFFFFFFFFFFFF {
			lastGranule = granule
		}

		// Collect loudness for waveform (skip header pages)
		if foundHead && granule > 0 && granule != 0xFFFFFFFFFFFFFFFF {
			loudness = append(loudness, computePageLoudness(payload))
		}

		offset = newOffset
	}

	if lastGranule == 0 {
		return 0, nil, errors.New("no audio granules found")
	}

	// Opus uses 48000 Hz sample rate
	samples := lastGranule - uint64(preSkip)
	duration := uint32(samples / 48000)
	if duration == 0 {
		duration = 1
	}

	waveform := buildWaveform(loudness)
	return duration, waveform, nil
}

// readOggPage reads one OGG page starting at offset. Returns the page number,
// granule position, concatenated segment payload, and the next offset.
func readOggPage(data []byte, offset int) (uint32, uint64, []byte, int, error) {
	// Minimum OGG page header: 27 bytes
	if offset+27 > len(data) {
		return 0, 0, nil, 0, errors.New("truncated page header")
	}

	// Check capture pattern "OggS"
	if string(data[offset:offset+4]) != "OggS" {
		return 0, 0, nil, 0, errors.New("not an OGG page")
	}

	granule := binary.LittleEndian.Uint64(data[offset+6 : offset+14])
	pageSeq := binary.LittleEndian.Uint32(data[offset+18 : offset+22])
	numSegments := int(data[offset+26])

	if offset+27+numSegments > len(data) {
		return 0, 0, nil, 0, errors.New("truncated segment table")
	}

	// Read segment table to compute total payload size
	segTable := data[offset+27 : offset+27+numSegments]
	payloadSize := 0
	for _, s := range segTable {
		payloadSize += int(s)
	}

	payloadStart := offset + 27 + numSegments
	if payloadStart+payloadSize > len(data) {
		return 0, 0, nil, 0, errors.New("truncated payload")
	}

	payload := data[payloadStart : payloadStart+payloadSize]
	nextOffset := payloadStart + payloadSize

	return pageSeq, granule, payload, nextOffset, nil
}

// computePageLoudness calculates an RMS-proxy loudness for a page payload.
// Treats raw bytes as unsigned amplitude samples centered at 128.
func computePageLoudness(payload []byte) float64 {
	if len(payload) == 0 {
		return 0
	}
	var sumSq float64
	for _, b := range payload {
		v := float64(b) - 128.0
		sumSq += v * v
	}
	return math.Sqrt(sumSq / float64(len(payload)))
}

// buildWaveform resamples loudness values to exactly 64 bins normalized to [0,100].
func buildWaveform(loudness []float64) []byte {
	const bins = 64
	waveform := make([]byte, bins)

	if len(loudness) == 0 {
		return waveform
	}

	// Find max for normalization
	maxVal := 0.0
	for _, v := range loudness {
		if v > maxVal {
			maxVal = v
		}
	}
	if maxVal == 0 {
		return waveform
	}

	// Resample using linear interpolation
	for i := 0; i < bins; i++ {
		// Map bin index to source position
		srcPos := float64(i) * float64(len(loudness)-1) / float64(bins-1)
		srcIdx := int(srcPos)
		frac := srcPos - float64(srcIdx)

		var val float64
		if srcIdx+1 < len(loudness) {
			val = loudness[srcIdx]*(1-frac) + loudness[srcIdx+1]*frac
		} else {
			val = loudness[srcIdx]
		}

		normalized := val / maxVal * 100.0
		if normalized > 100 {
			normalized = 100
		}
		waveform[i] = byte(normalized)
	}

	return waveform
}
