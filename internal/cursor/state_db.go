package cursor

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"cursor/internal/appdata"
	"cursor/internal/logger"

	_ "modernc.org/sqlite"
)

const (
	cursorStateMembershipType      = "ultra"
	cursorStateSubscriptionStatus  = "active"
	cursorStateDefaultSignUpType   = "Google"
	cursorStateSQLiteBusyTimeoutMS = 2000
	cursorStateDBRelativePath      = "Cursor/User/globalStorage/state.vscdb"
	cursorStateDarwinRelativePath  = "Library/Application Support/Cursor/User/globalStorage/state.vscdb"
	cursorStateLinuxRelativePath   = ".config/Cursor/User/globalStorage/state.vscdb"
	cursorStateStatsigBootstrapKey = "workbench.experiments.statsigBootstrap"
	// cursorAuthBackupFileName 保存 Cursor 官方账号状态备份（注入模拟账号前抓取），
	// 供停止服务/直连模式时恢复官方登录态。内容为 cursorAuth/* 的原始值，
	// null 表示「注入前该键不存在或已是注入值」，恢复时删除该键。
	cursorAuthBackupFileName = "cursor-auth-backup.json"
)

// cursorAuthBackupKeys 是需要备份/恢复的核心账号键。
// cachedSignUpType/stripeMembershipType/stripeSubscriptionStatus 由 Cursor 依据
// 官方 token 自行维护，恢复时统一删除让客户端重新拉取，不参与备份。
var cursorAuthBackupKeys = []string{
	"cursorAuth/accessToken",
	"cursorAuth/refreshToken",
	"cursorAuth/cachedEmail",
}

// cursorAuthInjectedKeys 是本项目注入的完整键集合，恢复时若备份无值则全部删除。
var cursorAuthInjectedKeys = []string{
	"cursorAuth/accessToken",
	"cursorAuth/refreshToken",
	"cursorAuth/cachedEmail",
	"cursorAuth/cachedSignUpType",
	"cursorAuth/stripeMembershipType",
	"cursorAuth/stripeSubscriptionStatus",
}

var cursorStateDisabledStatsigGates = []string{
	"decompose_always_local_ext_host",
	"cursor_extensions_isolation_v2",
}

// CursorAuthImportResult 描述隔离实例的最小登录态导入结果，不包含任何登录凭据。
type CursorAuthImportResult struct {
	ImportedKeyCount int
}

// ImportCursorAuthState 从真实 Cursor 状态库只读导入最小登录态到隔离状态库。
// 它只处理 cursorAuthBackupKeys，不复制其他 Cursor 数据，也不会修改 Statsig 配置。
func ImportCursorAuthState(sourcePath, destinationPath string) (CursorAuthImportResult, error) {
	sourcePath, err := filepath.Abs(sourcePath)
	if err != nil {
		return CursorAuthImportResult{}, errors.New("定位真实 Cursor 登录态失败")
	}
	destinationPath, err = filepath.Abs(destinationPath)
	if err != nil {
		return CursorAuthImportResult{}, errors.New("定位隔离 Cursor 登录态失败")
	}
	if sameCursorStateDBPath(sourcePath, destinationPath) {
		return CursorAuthImportResult{}, errors.New("隔离 Cursor 登录态路径不能与真实状态库相同")
	}
	if info, err := os.Stat(sourcePath); err != nil || info.IsDir() {
		return CursorAuthImportResult{}, errors.New("读取真实 Cursor 登录态失败")
	}
	if _, err := os.Stat(destinationPath); err == nil {
		return CursorAuthImportResult{}, errors.New("隔离 Cursor 登录态已存在")
	} else if !errors.Is(err, os.ErrNotExist) {
		return CursorAuthImportResult{}, errors.New("检查隔离 Cursor 登录态失败")
	}

	values, err := readCursorAuthStateReadOnly(sourcePath)
	if err != nil {
		return CursorAuthImportResult{}, err
	}
	if err := writeIsolatedCursorAuthState(destinationPath, values); err != nil {
		return CursorAuthImportResult{}, err
	}
	return CursorAuthImportResult{ImportedKeyCount: len(values)}, nil
}

func sameCursorStateDBPath(left, right string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func readCursorAuthStateReadOnly(sourcePath string) (map[string]string, error) {
	sourceURIPath := filepath.ToSlash(sourcePath)
	if runtime.GOOS == "windows" && !strings.HasPrefix(sourceURIPath, "/") {
		// Windows 绝对路径需要 file:///C:/...，否则 C: 会被 URI 解析为主机。
		sourceURIPath = "/" + sourceURIPath
	}
	sourceURL := url.URL{
		Scheme:   "file",
		Path:     sourceURIPath,
		RawQuery: "mode=ro",
	}
	db, err := sql.Open("sqlite", sourceURL.String())
	if err != nil {
		return nil, errors.New("读取真实 Cursor 登录态失败")
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	ctx := context.Background()
	if _, err := db.ExecContext(ctx, fmt.Sprintf("PRAGMA busy_timeout = %d", cursorStateSQLiteBusyTimeoutMS)); err != nil {
		return nil, errors.New("读取真实 Cursor 登录态失败")
	}

	values := make(map[string]string, len(cursorAuthBackupKeys))
	for _, key := range cursorAuthBackupKeys {
		var raw []byte
		err := db.QueryRowContext(ctx, "SELECT value FROM ItemTable WHERE key = ?", key).Scan(&raw)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, errors.New("真实 Cursor 登录态不完整")
			}
			return nil, errors.New("读取真实 Cursor 登录态失败")
		}
		value := strings.TrimSpace(string(raw))
		if value == "" {
			return nil, errors.New("真实 Cursor 登录态不完整")
		}
		values[key] = value
	}
	return values, nil
}

// CursorAuthValues is the authentication whitelist read from Cursor's state DB.
type CursorAuthValues struct {
	AccessToken  string
	RefreshToken string
	Email        string
}

// CursorStateDBPath returns the current platform's Cursor state.vscdb path.
func CursorStateDBPath() (string, error) {
	return resolveCursorStateDBPath()
}

// ReadCursorAuth reads cursorAuth access/refresh/email keys from a state DB
// in read-only mode. It does not write the database.
func ReadCursorAuth(path string) (CursorAuthValues, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return CursorAuthValues{}, fmt.Errorf("cursor state db path is empty")
	}
	info, err := os.Stat(path)
	if err != nil {
		return CursorAuthValues{}, err
	}
	if info.IsDir() {
		return CursorAuthValues{}, os.ErrNotExist
	}
	values, err := readCursorAuthStateReadOnly(path)
	if err != nil {
		return CursorAuthValues{}, err
	}
	return CursorAuthValues{
		AccessToken:  strings.TrimSpace(values["cursorAuth/accessToken"]),
		RefreshToken: strings.TrimSpace(values["cursorAuth/refreshToken"]),
		Email:        strings.TrimSpace(values["cursorAuth/cachedEmail"]),
	}, nil
}

func writeIsolatedCursorAuthState(destinationPath string, values map[string]string) error {
	if err := os.MkdirAll(filepath.Dir(destinationPath), 0o700); err != nil {
		return errors.New("创建隔离 Cursor 登录态失败")
	}
	db, err := sql.Open("sqlite", destinationPath)
	if err != nil {
		return errors.New("创建隔离 Cursor 登录态失败")
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	ctx := context.Background()
	if _, err := db.ExecContext(ctx, fmt.Sprintf("PRAGMA busy_timeout = %d", cursorStateSQLiteBusyTimeoutMS)); err != nil {
		return errors.New("写入隔离 Cursor 登录态失败")
	}
	if _, err := db.ExecContext(ctx, "CREATE TABLE ItemTable (key TEXT UNIQUE ON CONFLICT REPLACE, value BLOB)"); err != nil {
		return errors.New("创建隔离 Cursor 登录态失败")
	}

	tx, err := db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return errors.New("写入隔离 Cursor 登录态失败")
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	for _, key := range cursorAuthBackupKeys {
		if _, err := tx.ExecContext(ctx, "INSERT INTO ItemTable(key, value) VALUES(?, ?)", key, values[key]); err != nil {
			return errors.New("写入隔离 Cursor 登录态失败")
		}
	}
	if err := tx.Commit(); err != nil {
		return errors.New("写入隔离 Cursor 登录态失败")
	}
	committed = true
	return nil
}

// InjectCursorUserInfo synchronizes the Cursor user-level auth cache used by the
// Settings page. It does not modify the installed Cursor app bundle.
func InjectCursorUserInfo(email, token string) error {
	stateDBPath, err := resolveCursorStateDBPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(stateDBPath), 0o755); err != nil {
		return fmt.Errorf("创建 Cursor 状态目录失败: %w", err)
	}

	// 注入前备份官方账号状态（仅在备份不存在时抓取一次），
	// 供停止服务/直连模式时 RestoreCursorUserInfo 恢复。
	if err := backupCursorAuthState(stateDBPath, token, email); err != nil {
		logger.Errorf("backupCursorAuthState failed: %v", err)
	}

	values := buildCursorAuthStateValues(email, token)
	if err := syncCursorAuthStateDB(stateDBPath, values); err != nil {
		return fmt.Errorf("同步 Cursor 状态库失败 path=%s: %w", stateDBPath, err)
	}

	logger.Infof(
		"injectCursorUserInfo synced path=%s email=%s membership=%s subscription=%s disabled_statsig_gates=%s",
		stateDBPath,
		values["cursorAuth/cachedEmail"],
		values["cursorAuth/stripeMembershipType"],
		values["cursorAuth/stripeSubscriptionStatus"],
		strings.Join(cursorStateDisabledStatsigGates, ","),
	)
	return nil
}

func buildCursorAuthStateValues(email, token string) map[string]string {
	email = strings.TrimSpace(email)
	token = strings.TrimSpace(token)

	return map[string]string{
		"cursorAuth/accessToken":              token,
		"cursorAuth/cachedEmail":              email,
		"cursorAuth/cachedSignUpType":         cursorStateDefaultSignUpType,
		"cursorAuth/refreshToken":             token,
		"cursorAuth/stripeMembershipType":     cursorStateMembershipType,
		"cursorAuth/stripeSubscriptionStatus": cursorStateSubscriptionStatus,
	}
}

func syncCursorAuthStateDB(path string, values map[string]string) error {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	ctx := context.Background()
	if _, err := db.ExecContext(ctx, fmt.Sprintf("PRAGMA busy_timeout = %d", cursorStateSQLiteBusyTimeoutMS)); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS ItemTable (key TEXT UNIQUE ON CONFLICT REPLACE, value BLOB)"); err != nil {
		return err
	}

	tx, err := db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	stmt, err := tx.PrepareContext(ctx, "INSERT OR REPLACE INTO ItemTable(key, value) VALUES(?, ?)")
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, key := range keys {
		if _, err := stmt.ExecContext(ctx, key, values[key]); err != nil {
			return err
		}
	}

	if err := disableCursorStatsigGates(ctx, tx); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

// cursorAuthBackupPath 返回官方账号状态备份文件路径。
func cursorAuthBackupPath() string {
	return filepath.Join(appdata.DataRootPath(), cursorAuthBackupFileName)
}

// CursorAuthBackupPath 返回官方账号备份文件路径（导出版，供账号自动导入使用）。
func CursorAuthBackupPath() string {
	return cursorAuthBackupPath()
}

// ReadCursorAuthBackupValues 从官方账号备份映射中提取 accessToken/refreshToken/email。
// 键名契约（cursorAuth/accessToken 等）由此包统一维护，供 cursoraccount 自动导入消费。
func ReadCursorAuthBackupValues(backup map[string]any) (accessToken string, refreshToken string, email string) {
	if backup == nil {
		return "", "", ""
	}
	for _, key := range cursorAuthBackupKeys {
		text, _ := backup[key].(string)
		text = strings.TrimSpace(text)
		switch key {
		case "cursorAuth/accessToken":
			accessToken = text
		case "cursorAuth/refreshToken":
			refreshToken = text
		case "cursorAuth/cachedEmail":
			email = text
		}
	}
	return accessToken, refreshToken, email
}

// cursorAuthBackupHasRealValue 判断备份中是否存在任一非空官方值。
func cursorAuthBackupHasRealValue(backup map[string]any) bool {
	for _, key := range cursorAuthBackupKeys {
		if text, ok := backup[key].(string); ok && strings.TrimSpace(text) != "" {
			return true
		}
	}
	return false
}

// isInjectedCursorAuthValue 判断 state.vscdb 中某键的当前值是否就是我们注入的
// 模拟值（token/email 与注入参数一致）。若是，说明该键已被污染，备份记为 null。
func isInjectedCursorAuthValue(key, value, injectedToken, injectedEmail string) bool {
	switch key {
	case "cursorAuth/accessToken", "cursorAuth/refreshToken":
		return value != "" && value == injectedToken
	case "cursorAuth/cachedEmail":
		return value != "" && value == injectedEmail
	default:
		return false
	}
}

// backupCursorAuthState 在注入模拟账号前，把 state.vscdb 中 cursorAuth/* 的
// 官方值快照到应用数据目录：
//   - 无备份：直接抓取；
//   - 备份无官方值（全 null/损坏）：重新抓取；
//   - 已有有效备份但 state.vscdb 中的官方账号已变更（用户重新登录了其他账号）：
//     刷新备份，避免停止服务时恢复旧账号；
//   - 已有有效备份且账号未变：保留，不覆盖。
//
// 值为注入态/不存在时记为 null，恢复时删除对应键。
func backupCursorAuthState(stateDBPath, injectedToken, injectedEmail string) error {
	backupPath := cursorAuthBackupPath()
	if data, err := os.ReadFile(backupPath); err == nil {
		var existing map[string]any
		hasValidBackup := json.Unmarshal(data, &existing) == nil && cursorAuthBackupHasRealValue(existing)
		if hasValidBackup {
			changed, err := cursorAuthChangedSinceBackup(stateDBPath, existing, injectedToken, injectedEmail)
			if err != nil {
				logger.Errorf("backupCursorAuthState: 对比当前官方账号失败，保留既有备份 err=%v", err)
				return nil
			}
			if !changed {
				return nil // 账号未变更，保留既有备份
			}
			logger.Infof("backupCursorAuthState: 官方账号已变更，刷新备份 path=%s", backupPath)
		} else {
			// 备份存在但无官方值（首次备份时 state.vscdb 尚是注入态/损坏），
			// 用户重新登录官方后应允许重新抓取官方值，否则恢复会误删官方 token。
			logger.Infof("backupCursorAuthState: 既有备份无官方值，重新抓取 path=%s", backupPath)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	db, err := sql.Open("sqlite", stateDBPath)
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	rows, err := db.Query("SELECT key, value FROM ItemTable WHERE key LIKE 'cursorAuth/%'")
	if err != nil {
		return err
	}
	existing := map[string]string{}
	for rows.Next() {
		var key string
		var value []byte
		if err := rows.Scan(&key, &value); err != nil {
			rows.Close()
			return err
		}
		existing[key] = string(value)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	backup := map[string]any{}
	for _, key := range cursorAuthBackupKeys {
		value, ok := existing[key]
		if !ok || isInjectedCursorAuthValue(key, value, injectedToken, injectedEmail) {
			backup[key] = nil
		} else {
			backup[key] = value
		}
	}

	if err := os.MkdirAll(filepath.Dir(backupPath), 0o755); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(backup, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(backupPath, append(encoded, '\n'), 0o600); err != nil {
		return err
	}
	logger.Infof("backupCursorAuthState saved path=%s accessToken=%s cachedEmail=%s", backupPath, maskAuthToken(backup["cursorAuth/accessToken"]), backup["cursorAuth/cachedEmail"])
	return nil
}

// cursorAuthChangedSinceBackup 判断当前 state.vscdb 中的官方账号相对备份是否已变更
// （用户重新登录了其他官方账号）。当前值仍为注入模拟值时视为未变更，
// 避免把备份刷新成注入值/空值。任一备份键的当前值（排除注入态）与备份不一致即视为变更。
func cursorAuthChangedSinceBackup(stateDBPath string, backup map[string]any, injectedToken, injectedEmail string) (bool, error) {
	db, err := sql.Open("sqlite", stateDBPath)
	if err != nil {
		return false, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	changed := false
	for _, key := range cursorAuthBackupKeys {
		var raw []byte
		err := db.QueryRow("SELECT value FROM ItemTable WHERE key = ?", key).Scan(&raw)
		var current string
		switch {
		case err == nil:
			current = string(raw)
		case errors.Is(err, sql.ErrNoRows):
			current = ""
		default:
			return false, err
		}
		if isInjectedCursorAuthValue(key, current, injectedToken, injectedEmail) {
			continue // 当前仍是注入模拟值，不算真实账号变更
		}
		backupValue := ""
		if text, ok := backup[key].(string); ok {
			backupValue = strings.TrimSpace(text)
		}
		if strings.TrimSpace(current) != backupValue {
			changed = true
		}
	}
	return changed, nil
}

// maskAuthToken 脱敏备份日志中的 token：只输出长度与首 4 位，避免完整 token 落盘日志。
func maskAuthToken(raw any) string {
	s, _ := raw.(string)
	if strings.TrimSpace(s) == "" {
		return "<empty>"
	}
	if len(s) <= 8 {
		return fmt.Sprintf("<len=%d>", len(s))
	}
	return fmt.Sprintf("<len=%d prefix=%s>", len(s), s[:4])
}

// restoreCursorAuthStateDB 把指定键写回 state.vscdb（values），并删除 removes 中的键。
func restoreCursorAuthStateDB(path string, values map[string]string, removes []string) error {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	ctx := context.Background()
	if _, err := db.ExecContext(ctx, fmt.Sprintf("PRAGMA busy_timeout = %d", cursorStateSQLiteBusyTimeoutMS)); err != nil {
		return err
	}

	tx, err := db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	for key, value := range values {
		if _, err := tx.ExecContext(ctx, "INSERT OR REPLACE INTO ItemTable(key, value) VALUES(?, ?)", key, value); err != nil {
			return err
		}
	}
	for _, key := range removes {
		if _, err := tx.ExecContext(ctx, "DELETE FROM ItemTable WHERE key = ?", key); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

// RestoreCursorUserInfo 恢复 Cursor 官方登录态：
//   - 存在备份：写回官方 accessToken/refreshToken/cachedEmail，删除注入的其余键；
//   - 无备份（如旧版本已污染）：删除全部 cursorAuth/* 键，令 Cursor 回到未登录态，
//     用户重新登录官方账号即可。
//
// 供停止服务/退出/直连模式调用，确保本地模拟账号不会残留到官方连接。
func RestoreCursorUserInfo() error {
	stateDBPath, err := resolveCursorStateDBPath()
	if err != nil {
		return err
	}
	if _, err := os.Stat(stateDBPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			logger.Infof("restoreCursorUserInfo: state.vscdb 不存在，无需恢复 path=%s", stateDBPath)
			return nil
		}
		return err
	}

	backupPath := cursorAuthBackupPath()
	data, err := os.ReadFile(backupPath)
	values := map[string]string{}
	removes := []string{}
	switch {
	case err == nil:
		var backup map[string]any
		if err := json.Unmarshal(data, &backup); err != nil {
			logger.Infof("restoreCursorUserInfo: 备份解析失败，按清空处理 err=%v", err)
			removes = append(removes, cursorAuthInjectedKeys...)
			break
		}
		for _, key := range cursorAuthBackupKeys {
			raw, ok := backup[key]
			if !ok {
				continue
			}
			if text, ok := raw.(string); ok && strings.TrimSpace(text) != "" {
				values[key] = text
			} else {
				removes = append(removes, key)
			}
		}
		// 注入的展示/状态键始终删除，交由 Cursor 依据官方 token 重新生成。
		removes = append(removes,
			"cursorAuth/cachedSignUpType",
			"cursorAuth/stripeMembershipType",
			"cursorAuth/stripeSubscriptionStatus",
		)
	case errors.Is(err, os.ErrNotExist):
		logger.Infof("restoreCursorUserInfo: 无备份，清空全部 cursorAuth 键 path=%s", stateDBPath)
		removes = append(removes, cursorAuthInjectedKeys...)
	default:
		return err
	}

	if len(values) == 0 && len(removes) == 0 {
		return nil
	}
	if err := restoreCursorAuthStateDB(stateDBPath, values, removes); err != nil {
		return fmt.Errorf("恢复 Cursor 状态库失败 path=%s: %w", stateDBPath, err)
	}
	logger.Infof("restoreCursorUserInfo done path=%s restored=%d removed=%d", stateDBPath, len(values), len(removes))
	return nil
}

func disableCursorStatsigGates(ctx context.Context, tx *sql.Tx) error {
	var raw []byte
	err := tx.QueryRowContext(ctx, "SELECT value FROM ItemTable WHERE key = ?", cursorStateStatsigBootstrapKey).Scan(&raw)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil
		}
		return err
	}

	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return fmt.Errorf("解析 Cursor Statsig bootstrap 失败: %w", err)
	}
	if payload == nil {
		// DB 值可能为 JSON null，Unmarshal 后保持 nil，写入会 panic。
		payload = map[string]any{}
	}

	featureGates, _ := payload["feature_gates"].(map[string]any)
	if featureGates == nil {
		featureGates = map[string]any{}
		payload["feature_gates"] = featureGates
	}

	hashUsed, _ := payload["hash_used"].(string)
	for _, gate := range cursorStateDisabledStatsigGates {
		disableCursorStatsigGate(featureGates, gate)
		if strings.EqualFold(hashUsed, "djb2") {
			disableCursorStatsigGate(featureGates, cursorStateDJB2Hash(gate))
		}
	}

	updated, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("编码 Cursor Statsig bootstrap 失败: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "UPDATE ItemTable SET value = ? WHERE key = ?", updated, cursorStateStatsigBootstrapKey); err != nil {
		return err
	}
	return nil
}

func disableCursorStatsigGate(featureGates map[string]any, key string) {
	gate, _ := featureGates[key].(map[string]any)
	if gate == nil {
		gate = map[string]any{
			"name":       key,
			"rule_id":    "local_disabled",
			"ruleID":     "local_disabled",
			"group_name": "local_disabled",
			"groupName":  "local_disabled",
			"id_type":    "userID",
			"idType":     "userID",
		}
		featureGates[key] = gate
	}
	gate["value"] = false
}

func cursorStateDJB2Hash(value string) string {
	var hash uint32
	for _, b := range []byte(value) {
		hash = hash*31 + uint32(b)
	}
	return fmt.Sprintf("%d", hash)
}

func resolveCursorStateDBPath() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("获取用户目录失败: %w", err)
	}

	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(homeDir, filepath.FromSlash(cursorStateDarwinRelativePath)), nil
	case "windows":
		appData := strings.TrimSpace(os.Getenv("APPDATA"))
		if appData == "" {
			appData = filepath.Join(homeDir, "AppData", "Roaming")
		}
		return filepath.Join(appData, "Cursor", "User", "globalStorage", "state.vscdb"), nil
	case "linux":
		configDir := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME"))
		if configDir == "" {
			return filepath.Join(homeDir, filepath.FromSlash(cursorStateLinuxRelativePath)), nil
		}
		return filepath.Join(configDir, filepath.FromSlash(cursorStateDBRelativePath)), nil
	default:
		return "", fmt.Errorf("不支持的系统: %s", runtime.GOOS)
	}
}
