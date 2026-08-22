package cursor

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	// diskKVBlobKeyPrefix 是客户端 ComposerBlobStore 写入 cursorDiskKV 的内容寻址 blob 键前缀，
	// 完整键为 agentKv:blob:<sha256-hex>。
	diskKVBlobKeyPrefix = "agentKv:blob:"
	// diskKVQueryBatchSize 限制单条 IN 查询的键数量，避免超长 SQL。
	diskKVQueryBatchSize = 300
)

// ReadDiskKVBlobs 按小写 hex id 批量读取当前平台 Cursor 状态库 cursorDiskKV 表中的
// 内容寻址 blob。缺失的 id 不会出现在返回映射中，也不视为错误。
// 只读访问，不修改客户端数据库；用于旧会话续聊时水合客户端本地 blob。
func ReadDiskKVBlobs(ids []string) (map[string][]byte, error) {
	return readCursorDiskKVBlobsFromPath("", ids)
}

// ReadDiskKVBlobsFromPath 与 ReadDiskKVBlobs 相同，但显式指定 state.vscdb 路径。
func ReadDiskKVBlobsFromPath(stateDBPath string, ids []string) (map[string][]byte, error) {
	return readCursorDiskKVBlobsFromPath(stateDBPath, ids)
}

func readCursorDiskKVBlobsFromPath(stateDBPath string, ids []string) (map[string][]byte, error) {
	keys := make([]string, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		key := diskKVBlobKeyPrefix + strings.ToLower(id)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	if len(keys) == 0 {
		return nil, nil
	}
	if strings.TrimSpace(stateDBPath) == "" {
		resolved, err := resolveCursorStateDBPath()
		if err != nil {
			return nil, err
		}
		stateDBPath = resolved
	}
	db, err := openCursorDiskKVReadOnly(stateDBPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	ctx := context.Background()
	values := make(map[string][]byte, len(keys))
	for start := 0; start < len(keys); start += diskKVQueryBatchSize {
		end := start + diskKVQueryBatchSize
		if end > len(keys) {
			end = len(keys)
		}
		chunk := keys[start:end]
		placeholders := strings.Repeat("?,", len(chunk))
		placeholders = placeholders[:len(placeholders)-1]
		args := make([]any, len(chunk))
		for i, key := range chunk {
			args[i] = key
		}
		rows, err := db.QueryContext(ctx, "SELECT key, value FROM cursorDiskKV WHERE key IN ("+placeholders+")", args...)
		if err != nil {
			return nil, fmt.Errorf("查询 cursorDiskKV 失败: %w", err)
		}
		for rows.Next() {
			var key string
			var value []byte
			if err := rows.Scan(&key, &value); err != nil {
				rows.Close()
				return nil, fmt.Errorf("读取 cursorDiskKV 行失败: %w", err)
			}
			if id, ok := strings.CutPrefix(key, diskKVBlobKeyPrefix); ok {
				values[id] = append([]byte(nil), value...)
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("遍历 cursorDiskKV 失败: %w", err)
		}
	}
	return values, nil
}

func openCursorDiskKVReadOnly(stateDBPath string) (*sql.DB, error) {
	uriPath := filepath.ToSlash(stateDBPath)
	if runtime.GOOS == "windows" && !strings.HasPrefix(uriPath, "/") {
		// Windows 绝对路径需要 file:///C:/...，否则 C: 会被 URI 解析为主机。
		uriPath = "/" + uriPath
	}
	sourceURL := url.URL{
		Scheme:   "file",
		Path:     uriPath,
		RawQuery: "mode=ro",
	}
	db, err := sql.Open("sqlite", sourceURL.String())
	if err != nil {
		return nil, fmt.Errorf("打开 Cursor 状态库失败: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if _, err := db.Exec("PRAGMA busy_timeout = 2000"); err != nil {
		db.Close()
		return nil, fmt.Errorf("设置 Cursor 状态库 busy_timeout 失败: %w", err)
	}
	return db, nil
}

