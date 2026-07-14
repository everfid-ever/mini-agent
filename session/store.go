package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sync"
)

var idPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// Store 管理多个独立 session:内存 map + JSON 文件持久化。
// 不同 session_id 之间历史、todo 状态完全隔离。
type Store struct {
	dir      string
	mu       sync.Mutex
	sessions map[string]*Session
}

func NewStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("创建 session 目录失败: %w", err)
	}
	return &Store{dir: dir, sessions: make(map[string]*Session)}, nil
}

// GetOrCreate 返回指定 id 的 session;优先内存,其次磁盘,都没有则新建。
func (st *Store) GetOrCreate(id string) (*Session, error) {
	if !idPattern.MatchString(id) {
		return nil, fmt.Errorf("非法 session id %q(只允许字母数字-_,最长64)", id)
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if s, ok := st.sessions[id]; ok {
		return s, nil
	}
	s := New(id)
	raw, err := os.ReadFile(st.path(id))
	if err == nil {
		if uerr := json.Unmarshal(raw, s); uerr != nil {
			return nil, fmt.Errorf("session 文件损坏 %s: %w", st.path(id), uerr)
		}
		s.ID = id
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	st.sessions[id] = s
	return s, nil
}

// Save 将 session 落盘,每轮对话结束后调用,支持断线后"随时接着聊"。
func (st *Store) Save(s *Session) error {
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(st.path(s.ID), raw, 0o644)
}

func (st *Store) path(id string) string {
	return filepath.Join(st.dir, id+".json")
}
