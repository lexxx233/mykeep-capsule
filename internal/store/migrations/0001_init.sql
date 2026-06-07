PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS schema_version (
  version    INTEGER NOT NULL,
  min_binary TEXT    NOT NULL DEFAULT '',
  updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS bank (
  bank_id    TEXT PRIMARY KEY,
  name       TEXT,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS memory (
  id          INTEGER PRIMARY KEY,
  bank_id     TEXT    NOT NULL REFERENCES bank(bank_id) ON DELETE CASCADE,
  content     TEXT    NOT NULL,
  fact_type   TEXT    NOT NULL DEFAULT 'experience',
  context     TEXT,
  document_id TEXT,
  created_at  INTEGER NOT NULL,
  event_at    INTEGER,
  event_end   INTEGER,
  metadata    TEXT,
  embedder    TEXT,
  enriched    INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_memory_bank_time ON memory(bank_id, event_at, created_at);

CREATE TABLE IF NOT EXISTS memory_tag (
  memory_id INTEGER NOT NULL REFERENCES memory(id) ON DELETE CASCADE,
  tag       TEXT    NOT NULL,
  PRIMARY KEY (memory_id, tag)
);
CREATE INDEX IF NOT EXISTS idx_memory_tag_tag ON memory_tag(tag);

CREATE TABLE IF NOT EXISTS embedding (
  memory_id INTEGER NOT NULL REFERENCES memory(id) ON DELETE CASCADE,
  bank_id   TEXT    NOT NULL,
  model     TEXT    NOT NULL,
  dim       INTEGER NOT NULL,
  vec       BLOB    NOT NULL,
  PRIMARY KEY (memory_id, model)
);
CREATE INDEX IF NOT EXISTS idx_embedding_bank_model ON embedding(bank_id, model);

CREATE TABLE IF NOT EXISTS entity (
  id      INTEGER PRIMARY KEY,
  bank_id TEXT NOT NULL REFERENCES bank(bank_id) ON DELETE CASCADE,
  name    TEXT NOT NULL,
  type    TEXT,
  UNIQUE (bank_id, name, type)
);
CREATE TABLE IF NOT EXISTS memory_entity (
  memory_id INTEGER NOT NULL REFERENCES memory(id) ON DELETE CASCADE,
  entity_id INTEGER NOT NULL REFERENCES entity(id) ON DELETE CASCADE,
  PRIMARY KEY (memory_id, entity_id)
);

CREATE VIRTUAL TABLE IF NOT EXISTS memory_fts USING fts5(
  content, content='memory', content_rowid='id', tokenize='porter unicode61'
);
CREATE TRIGGER IF NOT EXISTS memory_ai AFTER INSERT ON memory BEGIN
  INSERT INTO memory_fts(rowid, content) VALUES (new.id, new.content);
END;
CREATE TRIGGER IF NOT EXISTS memory_ad AFTER DELETE ON memory BEGIN
  INSERT INTO memory_fts(memory_fts, rowid, content) VALUES('delete', old.id, old.content);
END;
CREATE TRIGGER IF NOT EXISTS memory_au AFTER UPDATE ON memory BEGIN
  INSERT INTO memory_fts(memory_fts, rowid, content) VALUES('delete', old.id, old.content);
  INSERT INTO memory_fts(rowid, content) VALUES (new.id, new.content);
END;
