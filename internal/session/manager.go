// session 包 — 会话管理。每个会话存完整的对话历史（JSON 序列化到 SQLite）。
// 支持新建、列出、切换、恢复、删除。
package session

import (
	"database/sql"
	"encoding/json"
	"time"

	"lagent/internal/adapter"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

// Session — 一个会话。Messages 存的是完整对话历史，JSON 序列化存在 DB 里。
// 注意：如果消息量很大，每条消息都走 JSON 序列化会有点重，当前场景够用。
type Session struct {
	ID        string
	Name      string
	Messages  []adapter.Message
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Manager struct {
	db *sql.DB
}

func NewManager(dbPath string) (*Manager, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}
	if err := createSessionTable(db); err != nil {
		return nil, err
	}
	return &Manager{db: db}, nil
}

func createSessionTable(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS sessions (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL DEFAULT '',
		messages TEXT NOT NULL DEFAULT '[]',
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`)
	return err
}

func (m *Manager) NewSession(name string) (*Session, error) {
	id := uuid.New().String()
	now := time.Now()
	_, err := m.db.Exec(`INSERT INTO sessions (id, name, messages, created_at, updated_at) VALUES (?, ?, '[]', ?, ?)`,
		id, name, now, now)
	if err != nil {
		return nil, err
	}
	return &Session{ID: id, Name: name, CreatedAt: now, UpdatedAt: now}, nil
}

// ListSessions — 按更新时间倒序列出所有会话。
func (m *Manager) ListSessions() ([]Session, error) {
	rows, err := m.db.Query(`SELECT id, name, messages, created_at, updated_at FROM sessions ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sessions []Session
	for rows.Next() {
		var s Session
		var msgJSON string
		if err := rows.Scan(&s.ID, &s.Name, &msgJSON, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(msgJSON), &s.Messages) // 解析失败就留 nil，不报错
		sessions = append(sessions, s)
	}
	return sessions, nil
}

// GetSession — 按 ID 取单个会话。
func (m *Manager) GetSession(id string) (*Session, error) {
	row := m.db.QueryRow(`SELECT id, name, messages, created_at, updated_at FROM sessions WHERE id = ?`, id)
	var s Session
	var msgJSON string
	if err := row.Scan(&s.ID, &s.Name, &msgJSON, &s.CreatedAt, &s.UpdatedAt); err != nil {
		return nil, err
	}
	json.Unmarshal([]byte(msgJSON), &s.Messages)
	return &s, nil
}

// UpdateMessages — 把消息列表写回 DB。每次对话都调，保证历史不丢。
func (m *Manager) UpdateMessages(id string, msgs []adapter.Message) error {
	data, err := json.Marshal(msgs)
	if err != nil {
		return err
	}
	_, err = m.db.Exec(`UPDATE sessions SET messages = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, string(data), id)
	return err
}

func (m *Manager) DeleteSession(id string) error {
	_, err := m.db.Exec(`DELETE FROM sessions WHERE id = ?`, id)
	return err
}

func (m *Manager) SetName(id, name string) error {
	_, err := m.db.Exec(`UPDATE sessions SET name = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, name, id)
	return err
}

func (m *Manager) Close() error {
	return m.db.Close()
}
