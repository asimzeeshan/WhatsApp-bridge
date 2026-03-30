package connection

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store/sqlstore"
	waLog "go.mau.fi/whatsmeow/util/log"

	"github.com/mdp/qrterminal/v3"
	_ "github.com/mattn/go-sqlite3"
)

const (
	baseBackoffMs = 1000
	maxBackoffMs  = 60000
	maxRetries    = 20
)

// Manager handles WhatsApp connection lifecycle including QR auth,
// reconnection with exponential backoff, and ban detection.
type Manager struct {
	State     *StateTracker
	Keepalive *KeepaliveTracker
	Client    *whatsmeow.Client

	dataDir         string
	cooldownMinutes int
	stopCh          chan struct{}
	logger          *slog.Logger
}

func NewManager(dataDir string, cooldownMinutes int, logger *slog.Logger) *Manager {
	return &Manager{
		State:           NewStateTracker(),
		Keepalive:       NewKeepaliveTracker(3),
		dataDir:         dataDir,
		cooldownMinutes: cooldownMinutes,
		stopCh:          make(chan struct{}),
		logger:          logger,
	}
}

// CheckCooldown returns an error if a cooldown marker exists and hasn't expired.
func (m *Manager) CheckCooldown() error {
	marker := filepath.Join(m.dataDir, "cooldown")
	info, err := os.Stat(marker)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("check cooldown: %w", err)
	}
	elapsed := time.Since(info.ModTime())
	cooldown := time.Duration(m.cooldownMinutes) * time.Minute
	if elapsed < cooldown {
		remaining := cooldown - elapsed
		return fmt.Errorf("cooldown active: %s remaining (ban detected at %s)", remaining.Round(time.Second), info.ModTime().Format(time.RFC3339))
	}
	// Expired, remove it
	os.Remove(marker)
	return nil
}

func (m *Manager) writeCooldownMarker() {
	marker := filepath.Join(m.dataDir, "cooldown")
	os.WriteFile(marker, []byte(time.Now().Format(time.RFC3339)), 0644)
}

// Connect initializes the whatsmeow client, handles QR auth, and starts
// the connection. It blocks until connected or permanently failed.
func (m *Manager) Connect(ctx context.Context) error {
	if err := m.CheckCooldown(); err != nil {
		m.State.Set(PermanentlyDisconnected)
		return err
	}

	m.State.Set(Connecting)
	gen := m.State.NextGeneration()

	// Open device store
	dbPath := filepath.Join(m.dataDir, "device.db")
	container, err := sqlstore.New(ctx, "sqlite3",
		fmt.Sprintf("file:%s?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=ON", dbPath),
		waLog.Noop,
	)
	if err != nil {
		m.State.Set(Disconnected)
		return fmt.Errorf("open device store: %w", err)
	}

	device, err := container.GetFirstDevice(ctx)
	if err != nil {
		m.State.Set(Disconnected)
		return fmt.Errorf("get device: %w", err)
	}

	client := whatsmeow.NewClient(device, waLog.Noop)
	m.Client = client

	// If not registered, need QR scan
	if client.Store.ID == nil {
		return m.connectWithQR(ctx, gen)
	}

	// Already registered, just connect
	err = client.Connect()
	if err != nil {
		m.State.Set(Disconnected)
		return fmt.Errorf("connect: %w", err)
	}

	m.State.Set(Connected)
	m.Keepalive.Reset()
	m.logger.Info("connected to WhatsApp")
	return nil
}

func (m *Manager) connectWithQR(ctx context.Context, gen int64) error {
	m.State.Set(QRWaiting)

	qrChan, err := m.Client.GetQRChannel(ctx)
	if err != nil {
		m.State.Set(Disconnected)
		return fmt.Errorf("get QR channel: %w", err)
	}
	err = m.Client.Connect()
	if err != nil {
		m.State.Set(Disconnected)
		return fmt.Errorf("connect for QR: %w", err)
	}

	for evt := range qrChan {
		if m.State.Generation() != gen {
			return fmt.Errorf("stale generation, aborting QR flow")
		}
		switch evt.Event {
		case "code":
			m.logger.Info("scan QR code with WhatsApp (Linked Devices > Link a Device)")
			fmt.Fprintln(os.Stderr)
			qrterminal.GenerateHalfBlock(evt.Code, qrterminal.L, os.Stderr)
			fmt.Fprintln(os.Stderr)
		case "success":
			m.State.Set(Connected)
			m.Keepalive.Reset()
			m.logger.Info("QR code scanned, connected")
			return nil
		case "timeout":
			m.State.Set(Disconnected)
			return fmt.Errorf("QR code scan timed out")
		}
	}
	return fmt.Errorf("QR channel closed unexpectedly")
}

// Reconnect attempts to re-establish the connection with exponential backoff.
// It respects the generation counter to prevent stale goroutines.
func (m *Manager) Reconnect(ctx context.Context) {
	gen := m.State.NextGeneration()

	for attempt := 0; attempt < maxRetries; attempt++ {
		select {
		case <-m.stopCh:
			return
		case <-ctx.Done():
			return
		default:
		}

		if m.State.Generation() != gen {
			m.logger.Debug("stale reconnect generation, stopping")
			return
		}

		delayMs := math.Min(float64(baseBackoffMs)*math.Pow(2, float64(attempt)), float64(maxBackoffMs))
		jitter := rand.IntN(int(delayMs) / 4)
		totalDelay := time.Duration(int(delayMs)+jitter) * time.Millisecond

		m.logger.Info("reconnecting", "attempt", attempt+1, "delay", totalDelay)
		m.State.Set(Connecting)

		select {
		case <-time.After(totalDelay):
		case <-m.stopCh:
			return
		case <-ctx.Done():
			return
		}

		if m.Client != nil {
			m.Client.Disconnect()
		}

		err := m.Connect(ctx)
		if err == nil {
			m.logger.Info("reconnected successfully")
			return
		}

		m.logger.Warn("reconnect failed", "error", err, "attempt", attempt+1)

		if m.State.Get() == PermanentlyDisconnected {
			return
		}
	}

	m.logger.Error("max reconnect attempts reached")
	m.State.Set(Disconnected)
}

// HandlePermanentDisconnect marks the connection as permanently failed
// and writes a cooldown marker if it was a ban.
func (m *Manager) HandlePermanentDisconnect(reason string, writeCooldown bool) {
	m.logger.Error("permanent disconnect", "reason", reason)
	m.State.Set(PermanentlyDisconnected)
	if writeCooldown {
		m.writeCooldownMarker()
	}
}

// Stop signals the manager to stop all reconnection attempts.
func (m *Manager) Stop() {
	select {
	case <-m.stopCh:
	default:
		close(m.stopCh)
	}
	if m.Client != nil {
		m.Client.Disconnect()
	}
}

// Stopped returns the stop channel for select statements.
func (m *Manager) Stopped() <-chan struct{} {
	return m.stopCh
}
