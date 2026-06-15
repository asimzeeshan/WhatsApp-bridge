package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// PollOption represents a single option in a poll.
type PollOption struct {
	Name string `json:"name"`
	Hash string `json:"hash"` // SHA-256 hex of the option name
}

// Poll represents a poll creation message.
type Poll struct {
	MessageID     string       `json:"message_id"`
	ChatJID       string       `json:"chat_jid"`
	Creator       string       `json:"creator"`
	Question      string       `json:"question"`
	Options       []PollOption `json:"options"`
	MaxSelections int          `json:"max_selections"`
	EncKey        []byte       `json:"-"`
	CreatedAt     int64        `json:"created_at"`
}

// PollVote represents a vote on a poll.
type PollVote struct {
	PollMessageID   string   `json:"poll_message_id"`
	ChatJID         string   `json:"chat_jid"`
	Voter           string   `json:"voter"`
	VoterName       string   `json:"voter_name"`
	SelectedOptions []string `json:"selected_options"`
	VotedAt         int64    `json:"voted_at"`
}

// HashPollOptionName returns the hex-encoded SHA-256 hash of an option name.
func HashPollOptionName(name string) string {
	h := sha256.Sum256([]byte(name))
	return hex.EncodeToString(h[:])
}

// UpsertPoll inserts or updates a poll.
func (db *DB) UpsertPoll(p *Poll) {
	// Compute hashes for each option
	for i := range p.Options {
		if p.Options[i].Hash == "" {
			p.Options[i].Hash = HashPollOptionName(p.Options[i].Name)
		}
	}

	optionsJSON, err := json.Marshal(p.Options)
	if err != nil {
		db.logger.Warn("failed to marshal poll options", "error", err)
		return
	}

	db.Exec(`
		INSERT INTO polls (message_id, chat_jid, creator, question, options, max_selections, enc_key, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(message_id, chat_jid) DO UPDATE SET
			creator = excluded.creator,
			question = excluded.question,
			options = excluded.options,
			max_selections = excluded.max_selections,
			enc_key = excluded.enc_key`,
		p.MessageID, p.ChatJID, p.Creator, p.Question, string(optionsJSON),
		p.MaxSelections, p.EncKey, p.CreatedAt,
	)
}

// UpsertPollVote inserts or updates a poll vote. Re-votes overwrite.
func (db *DB) UpsertPollVote(v *PollVote) {
	selectedJSON, err := json.Marshal(v.SelectedOptions)
	if err != nil {
		db.logger.Warn("failed to marshal selected options", "error", err)
		return
	}

	db.Exec(`
		INSERT INTO poll_votes (poll_message_id, chat_jid, voter, voter_name, selected_options, voted_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(poll_message_id, chat_jid, voter) DO UPDATE SET
			voter_name = excluded.voter_name,
			selected_options = excluded.selected_options,
			voted_at = excluded.voted_at`,
		v.PollMessageID, v.ChatJID, v.Voter, v.VoterName, string(selectedJSON), v.VotedAt,
	)
}

// GetPoll retrieves a poll by message ID and chat JID.
func (db *DB) GetPoll(messageID, chatJID string) (*Poll, error) {
	row := db.QueryRow(`
		SELECT message_id, chat_jid, creator, question, options, max_selections, enc_key, created_at
		FROM polls WHERE message_id = ? AND chat_jid = ?`, messageID, chatJID)

	var p Poll
	var optionsJSON string
	err := row.Scan(&p.MessageID, &p.ChatJID, &p.Creator, &p.Question, &optionsJSON,
		&p.MaxSelections, &p.EncKey, &p.CreatedAt)
	if err != nil {
		return nil, err
	}

	if err := json.Unmarshal([]byte(optionsJSON), &p.Options); err != nil {
		return nil, err
	}

	return &p, nil
}

// ResolveVoteHashes maps SHA-256 vote hashes (raw bytes) to option names.
func ResolveVoteHashes(selectedHashes [][]byte, options []PollOption) []string {
	var resolved []string
	for _, hash := range selectedHashes {
		hexHash := hex.EncodeToString(hash)
		for _, opt := range options {
			if opt.Hash == hexHash {
				resolved = append(resolved, opt.Name)
				break
			}
		}
	}
	return resolved
}
