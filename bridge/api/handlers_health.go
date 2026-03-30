package api

import (
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

type HealthResponse struct {
	Uptime         string  `json:"uptime"`
	GoMemoryMB     float64 `json:"go_memory_mb"`
	GoGoroutines   int     `json:"go_goroutines"`
	MessageCount   int     `json:"message_count"`
	ChatCount      int     `json:"chat_count"`
	ContactCount   int     `json:"contact_count"`
	DBSizeMB       float64 `json:"db_size_mb"`
	DataDirSizeMB  float64 `json:"data_dir_size_mb"`
	MediaDirSizeMB float64 `json:"media_dir_size_mb"`
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	var msgCount, chatCount, contactCount int
	s.db.Read.QueryRow("SELECT COUNT(*) FROM messages").Scan(&msgCount)
	s.db.Read.QueryRow("SELECT COUNT(*) FROM chats").Scan(&chatCount)
	s.db.Read.QueryRow("SELECT COUNT(*) FROM contacts").Scan(&contactCount)

	// Database size
	var dbSizeMB float64
	if s.db.IsPostgres() {
		var dbSize int64
		s.db.Read.QueryRow("SELECT pg_database_size(current_database())").Scan(&dbSize)
		dbSizeMB = float64(dbSize) / (1024 * 1024)
	} else {
		var pageCount, pageSize int64
		s.db.Read.QueryRow("PRAGMA page_count").Scan(&pageCount)
		s.db.Read.QueryRow("PRAGMA page_size").Scan(&pageSize)
		dbSizeMB = float64(pageCount*pageSize) / (1024 * 1024)
	}

	// Data directory size
	dataDirMB := dirSizeMB(s.cfg.Bridge.DataDir)

	// Media directory size
	mediaDirMB := dirSizeMB(expandTilde(s.cfg.Bridge.Media.ImageOutputDir))

	writeJSON(w, http.StatusOK, HealthResponse{
		Uptime:         time.Since(s.startedAt).Round(time.Second).String(),
		GoMemoryMB:     float64(memStats.Alloc) / (1024 * 1024),
		GoGoroutines:   runtime.NumGoroutine(),
		MessageCount:   msgCount,
		ChatCount:      chatCount,
		ContactCount:   contactCount,
		DBSizeMB:       dbSizeMB,
		DataDirSizeMB:  dataDirMB,
		MediaDirSizeMB: mediaDirMB,
	})
}

// dirSizeMB walks a directory and returns its total size in megabytes.
func dirSizeMB(path string) float64 {
	var size int64
	filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		size += info.Size()
		return nil
	})
	return float64(size) / (1024 * 1024)
}
