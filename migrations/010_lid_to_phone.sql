-- Migration: Convert LID-format JIDs to phone-format JIDs
-- This fixes false-flag push notifications caused by LID JIDs in MentionedJID
-- Safe to run multiple times (idempotent - only updates rows that still have @lid)

BEGIN;

-- Step 1: Create temporary mapping table from whatsmeow's LID store
CREATE TEMP TABLE lid_map (
    lid_jid  TEXT PRIMARY KEY,
    phone_jid TEXT NOT NULL
);

-- NOTE: in production this table is seeded from whatsmeow's own LID store.
-- The pairs below are illustrative placeholders only (no real numbers).
INSERT INTO lid_map (lid_jid, phone_jid) VALUES
('100000000000001@lid', '923001234567@s.whatsapp.net'),
('100000000000002@lid', '923007654321@s.whatsapp.net'),
('100000000000003@lid', '923009999999@s.whatsapp.net');

-- Also handle device-suffixed LIDs (e.g. 100000000000001:75@lid)
-- by extracting the base LID and creating additional mappings
INSERT INTO lid_map (lid_jid, phone_jid)
SELECT DISTINCT
    m.sender AS lid_jid,
    lm.phone_jid
FROM messages m
JOIN lid_map lm ON lm.lid_jid = (split_part(m.sender, ':', 1) || '@lid')
WHERE m.sender LIKE '%:%@lid'
  AND m.sender NOT IN (SELECT lid_jid FROM lid_map)
ON CONFLICT (lid_jid) DO NOTHING;

-- Step 2: Update messages.sender
UPDATE messages SET sender = lm.phone_jid
FROM lid_map lm
WHERE messages.sender = lm.lid_jid;

-- Step 3: Update messages.quoted_participant
UPDATE messages SET quoted_participant = lm.phone_jid
FROM lid_map lm
WHERE messages.quoted_participant = lm.lid_jid;

-- Also handle device-suffixed quoted_participant
UPDATE messages SET quoted_participant = lm.phone_jid
FROM lid_map lm
WHERE messages.quoted_participant LIKE '%:%@lid'
  AND lm.lid_jid = (split_part(messages.quoted_participant, ':', 1) || '@lid');

-- Step 4: Update reactions.reactor_jid
UPDATE reactions SET reactor_jid = lm.phone_jid
FROM lid_map lm
WHERE reactions.reactor_jid = lm.lid_jid;

-- Step 5: Update links.sender_jid
UPDATE links SET sender_jid = lm.phone_jid
FROM lid_map lm
WHERE links.sender_jid = lm.lid_jid;

-- Step 6: Merge LID contacts into phone contacts
-- For each LID contact that has a phone mapping, update the phone contact's
-- notify field if it's empty (LID contacts often have better push names)
UPDATE contacts c SET
    notify = COALESCE(NULLIF(c.notify, ''), lid_c.notify)
FROM contacts lid_c
JOIN lid_map lm ON lid_c.jid = lm.lid_jid
WHERE c.jid = lm.phone_jid
  AND c.notify = ''
  AND lid_c.notify != '';

-- Insert phone contacts for LIDs that don't have a phone contact yet
INSERT INTO contacts (jid, name, notify, phone)
SELECT lm.phone_jid, COALESCE(NULLIF(c.name, ''), ''), c.notify, ''
FROM contacts c
JOIN lid_map lm ON c.jid = lm.lid_jid
WHERE NOT EXISTS (SELECT 1 FROM contacts WHERE jid = lm.phone_jid)
ON CONFLICT (jid) DO NOTHING;

-- Remove LID-only contacts that now have a phone equivalent
DELETE FROM contacts
WHERE jid IN (SELECT lid_jid FROM lid_map)
  AND EXISTS (SELECT 1 FROM contacts c2 JOIN lid_map lm ON c2.jid = lm.phone_jid WHERE lm.lid_jid = contacts.jid);

-- Also remove device-suffixed LID contacts
DELETE FROM contacts WHERE jid LIKE '%:%@lid';

-- Step 7: Report results
DROP TABLE lid_map;

COMMIT;
