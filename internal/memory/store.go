// memory 包 — 长期记忆存储。
// 用 SQLite 存 key-value，按 user+project 维度隔离。
// 搜索用的是 LIKE 模糊匹配，README 写的 FTS5 还没实现。
// 每次对话前搜 Top-3 注入 prompt。
package memory

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Memory struct {
	ID         int64
	UserID     string
	Project    string
	Key        string
	Value      string
	Importance float64 // 0-1，数值越大越重要，排序时优先
	CreatedAt  time.Time
}

type Store struct {
	db *sql.DB
}

func NewStore(dbPath string) (*Store, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}
	if err := createTable(db); err != nil {
		return nil, err
	}
	return &Store{db: db}, nil
}

func createTable(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS memories (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id TEXT NOT NULL,
		project TEXT NOT NULL DEFAULT '',
		key TEXT NOT NULL,
		value TEXT NOT NULL,
		importance REAL DEFAULT 0.5,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`)
	return err
}

// Add — 添加一条记忆。importance 越高越靠前。
func (s *Store) Add(userID, project, key, value string, importance float64) error {
	_, err := s.db.Exec(
		`INSERT INTO memories (user_id, project, key, value, importance) VALUES (?,?,?,?,?)`,
		userID, project, key, value, importance,
	)
	return err
}

// Search — 双向子串匹配，取前 limit 条。
// 同时检查：记忆包含查询词（LIKE），以及查询词包含记忆 key/value（支持自然语言输入）。
// 数据量小的时候全量拉到内存里比对就行，大了再切 FTS5。
func (s *Store) Search(userID, project, query string, limit int) ([]Memory, error) {
	// 先把该用户+项目下的所有记忆捞出来
	rows, err := s.db.Query(`
		SELECT id, user_id, project, key, value, importance, created_at
		FROM memories
		WHERE user_id = ? AND project = ?
		ORDER BY importance DESC
	`, userID, project)
	if err != nil {
		return nil, fmt.Errorf("search memories: %w", err)
	}
	defer rows.Close()

	var all []Memory
	for rows.Next() {
		var m Memory
		if err := rows.Scan(&m.ID, &m.UserID, &m.Project, &m.Key, &m.Value, &m.Importance, &m.CreatedAt); err != nil {
			return nil, err
		}
		all = append(all, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// 双向匹配：
	//   "AEeix" 包含 "AEeix" → 命中（精确关键词搜索）
	//   "你好，你知道AEeix吗" 包含 "AEeix" → 命中（自然语言输入）
	var matched []Memory
	for _, m := range all {
		if strings.Contains(m.Key, query) || strings.Contains(m.Value, query) ||
			strings.Contains(query, m.Key) || strings.Contains(query, m.Value) {
			matched = append(matched, m)
		}
	}

	if len(matched) > limit {
		matched = matched[:limit]
	}
	return matched, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}
