-- Graph-ready: typed relationships between entities (PLAN §6). Populated only when
-- the agent supplies structured relations; the v2 graph recall arm reads it.
CREATE TABLE IF NOT EXISTS edge (
  bank_id   TEXT    NOT NULL,
  src       INTEGER NOT NULL REFERENCES entity(id) ON DELETE CASCADE,
  dst       INTEGER NOT NULL REFERENCES entity(id) ON DELETE CASCADE,
  relation  TEXT    NOT NULL,
  memory_id INTEGER REFERENCES memory(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_edge_src ON edge(bank_id, src);
