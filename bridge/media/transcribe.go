package media

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/asimzeeshan/WhatsApp-bridge/bridge/config"
)

// TranscriptionResult holds the Whisper API response.
type TranscriptionResult struct {
	Text string `json:"text"`
}

// Transcriber handles audio transcription via Whisper API.
type Transcriber struct {
	cfg    config.TranscriptionConfig
	client *http.Client
	logger *slog.Logger
}

func NewTranscriber(cfg config.TranscriptionConfig, logger *slog.Logger) *Transcriber {
	return &Transcriber{
		cfg: cfg,
		client: &http.Client{
			Timeout: 120 * time.Second, // Voice notes can take a while to transcribe
		},
		logger: logger,
	}
}

// convertToWAV converts audio data to WAV format using macOS afconvert.
// Returns the WAV data and a cleanup function, or the original data if conversion fails/unnecessary.
func (t *Transcriber) convertToWAV(audioData []byte, filename string) ([]byte, string, error) {
	ext := strings.ToLower(filepath.Ext(filename))

	// WAV files don't need conversion
	if ext == ".wav" {
		return audioData, filename, nil
	}

	// Write input to temp file
	tmpIn, err := os.CreateTemp("", "whisper-in-*"+ext)
	if err != nil {
		return audioData, filename, nil // fall back to original
	}
	defer os.Remove(tmpIn.Name())

	if _, err := tmpIn.Write(audioData); err != nil {
		tmpIn.Close()
		return audioData, filename, nil
	}
	tmpIn.Close()

	// Convert using macOS built-in afconvert (16kHz mono WAV - whisper's preferred format)
	wavPath := tmpIn.Name() + ".wav"
	cmd := exec.Command("afconvert", "-f", "WAVE", "-d", "LEI16@16000", tmpIn.Name(), wavPath)
	if err := cmd.Run(); err != nil {
		t.logger.Debug("afconvert failed, sending original format", "error", err)
		return audioData, filename, nil
	}
	defer os.Remove(wavPath)

	wavData, err := os.ReadFile(wavPath)
	if err != nil {
		return audioData, filename, nil
	}

	// Replace extension in filename
	wavFilename := strings.TrimSuffix(filename, ext) + ".wav"
	t.logger.Debug("converted audio to WAV", "from", ext, "size_in", len(audioData), "size_out", len(wavData))
	return wavData, wavFilename, nil
}

// Transcribe sends audio data to the Whisper API and returns the transcription text.
func (t *Transcriber) Transcribe(audioData []byte, filename string) (string, error) {
	if !t.cfg.Enabled {
		return "", nil
	}

	// Convert to WAV for whisper.cpp compatibility (uses macOS afconvert, no extra deps)
	sendData, sendFilename, _ := t.convertToWAV(audioData, filename)

	// Build multipart form
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	// Add audio file
	part, err := writer.CreateFormFile("file", sendFilename)
	if err != nil {
		return "", fmt.Errorf("create form file: %w", err)
	}
	if _, err := part.Write(sendData); err != nil {
		return "", fmt.Errorf("write audio data: %w", err)
	}

	// Add model
	if t.cfg.Model != "" {
		writer.WriteField("model", t.cfg.Model)
	}

	// Add language hint (empty = auto-detect)
	if t.cfg.Language != "" {
		writer.WriteField("language", t.cfg.Language)
	}

	// Response format
	writer.WriteField("response_format", "json")

	// Temperature (whisper.cpp requires this)
	writer.WriteField("temperature", "0.0")

	writer.Close()

	// Send request
	req, err := http.NewRequest("POST", t.cfg.WhisperURL, &buf)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := t.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("whisper request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("whisper returned %d: %s", resp.StatusCode, string(body))
	}

	var result TranscriptionResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}

	return strings.TrimSpace(result.Text), nil
}

// IsEnabled returns whether transcription is configured and enabled.
func (t *Transcriber) IsEnabled() bool {
	return t.cfg.Enabled && t.cfg.WhisperURL != ""
}
