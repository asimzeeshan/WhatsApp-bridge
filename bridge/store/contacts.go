package store

import "fmt"

type Contact struct {
	JID    string `json:"jid"`
	Name   string `json:"name"`
	Notify string `json:"notify,omitempty"`
	Phone  string `json:"phone,omitempty"`
}

func (db *DB) UpsertContact(c *Contact) {
	db.Exec(`
		INSERT INTO contacts (jid, name, notify, phone)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(jid) DO UPDATE SET
			name=CASE WHEN excluded.name != '' THEN excluded.name ELSE contacts.name END,
			notify=CASE WHEN excluded.notify != '' THEN excluded.notify ELSE contacts.notify END,
			phone=CASE WHEN excluded.phone != '' THEN excluded.phone ELSE contacts.phone END`,
		c.JID, c.Name, c.Notify, c.Phone)
}

func (db *DB) QueryContacts() ([]Contact, error) {
	rows, err := db.Query(`SELECT jid, name, notify, phone FROM contacts ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("query contacts: %w", err)
	}
	defer rows.Close()

	var contacts []Contact
	for rows.Next() {
		var c Contact
		if err := rows.Scan(&c.JID, &c.Name, &c.Notify, &c.Phone); err != nil {
			return nil, fmt.Errorf("scan contact: %w", err)
		}
		contacts = append(contacts, c)
	}
	return contacts, rows.Err()
}

func (db *DB) GetContact(jid string) (*Contact, error) {
	var c Contact
	err := db.QueryRow("SELECT jid, name, notify, phone FROM contacts WHERE jid = ?", jid).
		Scan(&c.JID, &c.Name, &c.Notify, &c.Phone)
	if err != nil {
		return nil, err
	}
	return &c, nil
}
