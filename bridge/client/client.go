package client

import (
	"log/slog"

	"go.mau.fi/whatsmeow"

	"github.com/asimzeeshan/WhatsApp-bridge/bridge/config"
	"github.com/asimzeeshan/WhatsApp-bridge/bridge/media"
	"github.com/asimzeeshan/WhatsApp-bridge/bridge/store"
)

// DisconnectHandler is called when a critical disconnect event occurs.
type DisconnectHandler func(reason string, writeCooldown bool)

// Bridge wraps the whatsmeow client with event handling, auto-download,
// and link indexing capabilities.
type Bridge struct {
	DB                    *store.DB
	Cfg                   *config.Config
	Logger                *slog.Logger
	waClient              *whatsmeow.Client
	Transcriber           *media.Transcriber
	OnPermanentDisconnect DisconnectHandler
}

func New(db *store.DB, cfg *config.Config, logger *slog.Logger) *Bridge {
	return &Bridge{
		DB:          db,
		Cfg:         cfg,
		Logger:      logger,
		Transcriber: media.NewTranscriber(cfg.Bridge.Transcription, logger),
	}
}

// RegisterEventHandlers attaches WhatsApp event handlers to the client.
func (b *Bridge) RegisterEventHandlers(client *whatsmeow.Client) {
	b.waClient = client
	client.AddEventHandler(b.handleEvent)
}
