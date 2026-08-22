package cursoraccount

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"cursor/gen/aiserverv1"
	"cursor/internal/appdata"
	"cursor/internal/backend/server/upstream"
	"cursor/internal/cursor"
	"cursor/internal/logger"

	"github.com/google/uuid"
	"github.com/pkg/browser"
	"google.golang.org/protobuf/proto"
)

const (
	StateSignedOut = "signed_out"
	StateWaiting   = "waiting"
	StateSignedIn  = "signed_in"
	StateError     = "error"

	websiteURL    = "https://cursor.com"
	backendURL    = "https://api2.cursor.sh"
	authClientID  = "KbZUR41cY7W6zRSdpSUJ7I7mLYBKOCmB"
	loginTimeout  = 10 * time.Minute
	pollInterval  = time.Second
	refreshMargin = 2 * time.Minute
)

var ErrNotSignedIn = errors.New("尚未在 cursor-byok 中登录 Cursor 账号")

type codedError struct {
	code string
	msg  string
	err  error
}

func (e *codedError) Error() string {
	if e == nil {
		return ""
	}
	if e.msg != "" {
		return e.msg
	}
	return e.code
}

func (e *codedError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func (e *codedError) Code() string {
	if e == nil {
		return ""
	}
	return e.code
}

func accountError(code, message string) error {
	return &codedError{code: code, msg: message}
}

func wrapAccountError(code, message string, err error) error {
	return &codedError{code: code, msg: message, err: err}
}

// Status 是可安全返回给前端的脱敏账号状态。
type Status struct {
	State  string `json:"state"`
	AuthID string `json:"authId"`
	Email  string `json:"email"`
	Error  string `json:"error"`
}

type credentials struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	AuthID       string `json:"authId"`
	Email        string `json:"email,omitempty"`
}

type pollResponse struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	AuthID       string `json:"authId"`
}

type refreshResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ShouldLogout bool   `json:"shouldLogout"`
}

// Manager 持有 cursor-byok 自己的 Cursor 登录态，不读写 Cursor 客户端状态库。
type Manager struct {
	store  *AccountStore
	client *http.Client

	mu              sync.RWMutex
	credentials     credentials
	state           string
	lastError       string
	loginCancel     context.CancelFunc
	loginGeneration uint64
	loginSessionID  string
	loginExpiresAt  int64

	refreshMu sync.Mutex
	// saveMu 串行化 save 写入（固定 .tmp 文件名并发竞争防护）。
	saveMu sync.Mutex
	// importOffMarkerPath 可覆盖的「主动断开」标记路径；空值使用默认 appdata 路径（测试注入用）。
	importOffMarkerPath string
	openLoginURL        func(string) error
	localAuthReader     func() (credentials, error)
	exportMu            sync.Mutex
	pendingExport       *pendingExport
	switchMu            sync.Mutex
	pendingSwitch       *pendingSwitch
	cursorRuntime       CursorRuntime
	stateDBPath         func() (string, error)
}

func NewManager(dataRoot, legacyPath string, client *http.Client) *Manager {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	manager := &Manager{
		store:  NewAccountStore(dataRoot, legacyPath),
		client: client,
		state:  StateSignedOut,
	}
	if err := manager.load(); err != nil {
		manager.state = StateError
		manager.lastError = fmt.Sprintf("读取 Cursor 账号凭据失败: %v", err)
	}
	return manager
}

func (manager *Manager) CurrentAccountID() string {
	if manager == nil || manager.store == nil {
		return ""
	}
	return manager.store.CurrentAccountID()
}

func (manager *Manager) ListAccounts() ([]CursorAccountSummary, error) {
	if manager == nil || manager.store == nil {
		return []CursorAccountSummary{}, fmt.Errorf("cursor account store is not initialized")
	}
	return manager.store.List()
}

func (manager *Manager) SetCurrent(id string) (CursorAccountSummary, error) {
	if manager == nil || manager.store == nil {
		return CursorAccountSummary{}, fmt.Errorf("cursor account store is not initialized")
	}
	if strings.TrimSpace(id) == "" {
		return CursorAccountSummary{}, accountError("account_not_found", "account id is empty")
	}
	summary, err := manager.store.SetCurrent(id)
	if err != nil {
		return CursorAccountSummary{}, err
	}
	manager.adoptCurrentFromStore()
	return summary, nil
}

func (manager *Manager) ImportFromLocal() (CursorAccountSummary, error) {
	if manager == nil || manager.store == nil {
		return CursorAccountSummary{}, fmt.Errorf("cursor account store is not initialized")
	}
	creds, err := manager.readLocalCursorAuth()
	if err != nil {
		return CursorAccountSummary{}, err
	}
	return manager.adoptImported(importedAccount{
		AccessToken:  creds.AccessToken,
		RefreshToken: creds.RefreshToken,
		AuthID:       creds.AuthID,
		Email:        creds.Email,
	})
}

func (manager *Manager) ImportToken(ctx context.Context, raw string) (CursorAccountSummary, error) {
	if manager == nil || manager.store == nil {
		return CursorAccountSummary{}, fmt.Errorf("cursor account store is not initialized")
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return CursorAccountSummary{}, accountError("account_import_empty", "import token is empty")
	}
	if len(raw) > maxImportTokenBytes {
		return CursorAccountSummary{}, accountError("account_import_too_large", "import token exceeds 8 kib")
	}
	creds := credentials{AccessToken: raw}
	if profile, err := manager.fetchProfile(ctx, bearer(raw)); err == nil {
		creds.Email = strings.TrimSpace(profile.GetEmail())
		creds.AuthID = strings.TrimSpace(profile.GetAuthId())
	}
	if creds.Email == "" && creds.AuthID == "" {
		return CursorAccountSummary{}, accountError("account_import_identity_unavailable", "account identity is unavailable")
	}
	return manager.adoptImported(importedAccount{
		AccessToken:  creds.AccessToken,
		RefreshToken: creds.RefreshToken,
		AuthID:       creds.AuthID,
		Email:        creds.Email,
	})
}

func (manager *Manager) ImportJSON(content string) ([]CursorAccountSummary, error) {
	if manager == nil || manager.store == nil {
		return []CursorAccountSummary{}, fmt.Errorf("cursor account store is not initialized")
	}
	accounts, err := parseImportJSON(content)
	if err != nil {
		return nil, err
	}
	summaries := make([]CursorAccountSummary, 0, len(accounts))
	for _, account := range accounts {
		summary, err := manager.adoptImported(account)
		if err != nil {
			return nil, err
		}
		summaries = append(summaries, summary)
	}
	return summaries, nil
}

func (manager *Manager) UpdateTags(id string, tags []string) (CursorAccountSummary, error) {
	if manager == nil || manager.store == nil {
		return CursorAccountSummary{}, fmt.Errorf("cursor account store is not initialized")
	}
	if strings.TrimSpace(id) == "" {
		return CursorAccountSummary{}, accountError("account_not_found", "account id is empty")
	}
	return manager.store.UpdateTags(id, tags)
}

func (manager *Manager) Delete(req CursorAccountDeleteRequest) error {
	if manager == nil || manager.store == nil {
		return fmt.Errorf("cursor account store is not initialized")
	}
	if err := manager.store.Delete(req); err != nil {
		return err
	}
	manager.adoptCurrentFromStore()
	return nil
}

// SeedAccountForTest upserts a fixture account that uses the test-access/test-refresh
// tokens. Production callers must not use this helper.
func (manager *Manager) SeedAccountForTest(authID, email string, setCurrent bool) (CursorAccountSummary, error) {
	if manager == nil || manager.store == nil {
		return CursorAccountSummary{}, fmt.Errorf("cursor account store is not initialized")
	}
	summary, err := manager.store.Upsert(credentials{
		AccessToken:  "test-access",
		RefreshToken: "test-refresh",
		AuthID:       authID,
		Email:        email,
	})
	if err != nil {
		return CursorAccountSummary{}, err
	}
	if !setCurrent {
		return summary, nil
	}
	summary, err = manager.store.SetCurrent(summary.ID)
	if err != nil {
		return CursorAccountSummary{}, err
	}
	manager.adoptCurrentFromStore()
	return summary, nil
}

func (manager *Manager) Status() Status {
	if manager == nil {
		return Status{State: StateSignedOut}
	}
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return Status{
		State:  manager.state,
		AuthID: manager.credentials.AuthID,
		Email:  manager.credentials.Email,
		Error:  manager.lastError,
	}
}

// EnsureEmail backfills a human-readable identity for credentials saved by
// builds that only persisted authId. Profile lookup failure does not invalidate
// an otherwise usable control-plane login.
func (manager *Manager) EnsureEmail(ctx context.Context) {
	if manager == nil || !manager.SignedIn() {
		return
	}
	current, accountID, err := manager.store.LoadCurrentCredentials()
	if err != nil || accountID == "" || strings.TrimSpace(current.Email) != "" {
		return
	}
	authorization, err := manager.Authorization(ctx)
	if err != nil {
		return
	}
	profile, err := manager.fetchProfile(ctx, authorization)
	if err != nil || strings.TrimSpace(profile.GetEmail()) == "" {
		return
	}
	if manager.store.CurrentAccountID() != accountID {
		return
	}
	current.Email = strings.TrimSpace(profile.GetEmail())
	if err := manager.store.UpdateCredentials(accountID, current); err != nil {
		return
	}
	if manager.store.CurrentAccountID() != accountID {
		return
	}
	manager.mu.Lock()
	manager.credentials.Email = current.Email
	manager.mu.Unlock()
}

func (manager *Manager) SignedIn() bool {
	if manager == nil {
		return false
	}
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.state == StateSignedIn && strings.TrimSpace(manager.credentials.AccessToken) != ""
}

// StartLogin 启动官方浏览器 PKCE 登录，并在后台等待登录结果。
func (manager *Manager) StartLogin() (Status, error) {
	if manager == nil {
		return Status{State: StateError}, fmt.Errorf("Cursor 账号服务未初始化")
	}
	verifierBytes := make([]byte, 32)
	if _, err := rand.Read(verifierBytes); err != nil {
		return manager.Status(), fmt.Errorf("生成 Cursor 登录校验码失败: %w", err)
	}
	verifier := base64.RawURLEncoding.EncodeToString(verifierBytes)
	challengeBytes := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(challengeBytes[:])
	loginID := uuid.NewString()
	sessionID := uuid.NewString()

	loginURL, err := buildLoginURL(loginID, challenge)
	if err != nil {
		return manager.Status(), err
	}
	ctx, cancel := context.WithTimeout(context.Background(), loginTimeout)

	manager.mu.Lock()
	if manager.loginCancel != nil {
		manager.loginCancel()
	}
	manager.loginGeneration++
	generation := manager.loginGeneration
	manager.loginCancel = cancel
	manager.loginSessionID = sessionID
	manager.loginExpiresAt = time.Now().Add(loginTimeout).UnixMilli()
	manager.state = StateWaiting
	manager.lastError = ""
	manager.mu.Unlock()

	if err := manager.openLoginPage(loginURL); err != nil {
		cancel()
		manager.finishWithError(generation, fmt.Sprintf("打开 Cursor 登录页面失败: %v", err))
		return manager.Status(), err
	}

	go manager.pollLogin(ctx, generation, loginID, verifier)
	return manager.Status(), nil
}

// Disconnect 只清除 cursor-byok 自己保存的账号，不调用 Cursor 客户端 logout。
func (manager *Manager) Disconnect() (Status, error) {
	if manager == nil {
		return Status{State: StateSignedOut}, nil
	}
	manager.mu.Lock()
	manager.loginGeneration++
	if manager.loginCancel != nil {
		manager.loginCancel()
		manager.loginCancel = nil
	}
	manager.credentials = credentials{}
	manager.state = StateSignedOut
	manager.lastError = ""
	manager.mu.Unlock()

	if manager.store != nil {
		if err := manager.store.clearCurrent(); err != nil && !errors.Is(err, os.ErrNotExist) {
			manager.mu.Lock()
			manager.state = StateError
			manager.lastError = fmt.Sprintf("清除 Cursor 账号凭据失败: %v", err)
			manager.mu.Unlock()
			return manager.Status(), err
		}
	}
	// 标记「主动断开」，阻止下次启动自动导入；手动 PKCE 登录会清除该标记。
	if markerErr := writeAutoImportOffMarker(manager.markerPath()); markerErr != nil {
		logger.Errorf("writeAutoImportOffMarker failed: %v", markerErr)
	}
	return manager.Status(), nil
}

func (manager *Manager) Shutdown() {
	if manager == nil {
		return
	}
	manager.mu.Lock()
	manager.loginGeneration++
	if manager.loginCancel != nil {
		manager.loginCancel()
		manager.loginCancel = nil
	}
	manager.mu.Unlock()
}

// Authorization 返回官方控制面请求使用的真实 Cursor Bearer 身份。
func (manager *Manager) Authorization(ctx context.Context) (string, error) {
	if manager == nil {
		return "", ErrNotSignedIn
	}
	manager.refreshMu.Lock()
	defer manager.refreshMu.Unlock()

	return manager.authorizationLocked(ctx)
}

func (manager *Manager) authorizationLocked(ctx context.Context) (string, error) {
	if manager.store == nil {
		return "", ErrNotSignedIn
	}
	const maxAttempts = 4
	for attempt := 0; attempt < maxAttempts; attempt++ {
		creds, accountID, err := manager.store.LoadCurrentCredentials()
		if err != nil {
			return "", err
		}
		if accountID == "" || strings.TrimSpace(creds.AccessToken) == "" {
			return "", ErrNotSignedIn
		}
		_, generation := manager.snapshotCredentials()
		if !tokenNeedsRefresh(creds.AccessToken, time.Now()) {
			manager.syncMemoryCredentials(creds, generation)
			return bearer(creds.AccessToken), nil
		}
		if strings.TrimSpace(creds.RefreshToken) == "" {
			if manager.store.CurrentAccountID() != accountID {
				continue
			}
			manager.setAuthorizationError(generation, "Cursor 登录已过期，请重新登录")
			return "", fmt.Errorf("Cursor 登录已过期且没有刷新令牌")
		}

		updated, shouldLogout, err := manager.refresh(ctx, creds)
		if err != nil {
			if manager.store.CurrentAccountID() != accountID {
				continue
			}
			manager.setAuthorizationError(generation, fmt.Sprintf("刷新 Cursor 登录失败: %v", err))
			return "", err
		}
		if manager.store.CurrentAccountID() != accountID {
			continue
		}
		if shouldLogout {
			manager.invalidateAuthorization(generation, "Cursor 登录已失效，请重新登录")
			return "", ErrNotSignedIn
		}
		if err := manager.store.UpdateCredentials(accountID, updated); err != nil {
			return "", err
		}
		manager.syncMemoryCredentials(updated, generation)
		return bearer(updated.AccessToken), nil
	}
	return "", fmt.Errorf("current account changed during refresh")
}

func (manager *Manager) pollLogin(ctx context.Context, generation uint64, loginID string, verifier string) {
	defer func() {
		manager.mu.Lock()
		if manager.loginGeneration == generation {
			manager.loginCancel = nil
		}
		manager.mu.Unlock()
	}()

	for {
		result, pending, err := manager.pollOnce(ctx, loginID, verifier)
		if err == nil && !pending {
			creds := credentials{
				AccessToken:  strings.TrimSpace(result.AccessToken),
				RefreshToken: strings.TrimSpace(result.RefreshToken),
				AuthID:       strings.TrimSpace(result.AuthID),
			}
			if creds.AccessToken == "" {
				manager.finishWithError(generation, "Cursor 登录响应缺少 access token")
				return
			}
			if profile, profileErr := manager.fetchProfile(ctx, bearer(creds.AccessToken)); profileErr == nil {
				creds.Email = strings.TrimSpace(profile.GetEmail())
			}
			_ = manager.commitOAuthCredentials(generation, creds)
			return
		}
		if err != nil && !isRetryablePollError(err) {
			manager.finishWithError(generation, fmt.Sprintf("Cursor 登录失败: %v", err))
			return
		}

		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				manager.finishWithError(generation, "Cursor 登录等待超时，请重试")
			}
			return
		case <-time.After(pollInterval):
		}
	}
}

func (manager *Manager) fetchProfile(ctx context.Context, authorization string) (*aiserverv1.GetMeResponse, error) {
	body, err := proto.Marshal(&aiserverv1.GetMeRequest{})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, backendURL+"/aiserver.v1.DashboardService/GetMe", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("authorization", authorization)
	req.Header.Set("x-cursor-checksum", upstream.BuildCursorChecksum(authorization))
	req.Header.Set("content-type", "application/proto")
	req.Header.Set("accept", "application/proto")
	req.Header.Set("connect-protocol-version", "1")
	resp, err := manager.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("GetMe 返回 HTTP %d", resp.StatusCode)
	}
	profile := &aiserverv1.GetMeResponse{}
	if err := proto.Unmarshal(responseBody, profile); err != nil {
		return nil, err
	}
	return profile, nil
}

func (manager *Manager) pollOnce(ctx context.Context, loginID string, verifier string) (pollResponse, bool, error) {
	endpoint, err := url.Parse(backendURL + "/auth/poll")
	if err != nil {
		return pollResponse{}, false, err
	}
	query := endpoint.Query()
	query.Set("uuid", loginID)
	query.Set("verifier", verifier)
	endpoint.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return pollResponse{}, false, err
	}
	resp, err := manager.client.Do(req)
	if err != nil {
		return pollResponse{}, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64*1024))
		return pollResponse{}, true, nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		return pollResponse{}, false, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return pollResponse{}, false, fmt.Errorf("登录服务返回 HTTP %d", resp.StatusCode)
	}
	result := pollResponse{}
	if err := json.Unmarshal(body, &result); err != nil {
		return pollResponse{}, false, fmt.Errorf("解析登录响应失败: %w", err)
	}
	return result, false, nil
}

func (manager *Manager) refresh(ctx context.Context, current credentials) (credentials, bool, error) {
	payload, err := json.Marshal(map[string]string{
		"grant_type":    "refresh_token",
		"client_id":     authClientID,
		"refresh_token": current.RefreshToken,
	})
	if err != nil {
		return credentials{}, false, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, backendURL+"/oauth/token", bytes.NewReader(payload))
	if err != nil {
		return credentials{}, false, err
	}
	req.Header.Set("content-type", "application/json")
	resp, err := manager.client.Do(req)
	if err != nil {
		return credentials{}, false, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		return credentials{}, false, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return credentials{}, false, fmt.Errorf("刷新服务返回 HTTP %d", resp.StatusCode)
	}
	result := refreshResponse{}
	if err := json.Unmarshal(body, &result); err != nil {
		return credentials{}, false, fmt.Errorf("解析刷新响应失败: %w", err)
	}
	if result.ShouldLogout {
		return credentials{}, true, nil
	}
	if strings.TrimSpace(result.AccessToken) == "" {
		return credentials{}, false, fmt.Errorf("刷新响应缺少 access token")
	}
	current.AccessToken = strings.TrimSpace(result.AccessToken)
	if strings.TrimSpace(result.RefreshToken) != "" {
		current.RefreshToken = strings.TrimSpace(result.RefreshToken)
	}
	return current, false, nil
}

func (manager *Manager) load() error {
	if manager.store == nil {
		return fmt.Errorf("cursor account store is not initialized")
	}
	loaded, _, err := manager.store.LoadCurrentCredentials()
	if err != nil {
		return err
	}
	loaded.AccessToken = strings.TrimSpace(loaded.AccessToken)
	loaded.RefreshToken = strings.TrimSpace(loaded.RefreshToken)
	loaded.AuthID = strings.TrimSpace(loaded.AuthID)
	loaded.Email = strings.TrimSpace(loaded.Email)
	if loaded.AccessToken == "" {
		return nil
	}
	manager.credentials = loaded
	manager.state = StateSignedIn
	return nil
}

// autoImportOffMarkerName 标记用户主动断开官方账号，阻止后续启动自动导入。
// 手动 PKCE 登录成功（commitCredentials）时删除该标记。
const autoImportOffMarkerName = "cursor-account.auto-import-off"

func autoImportOffMarkerPath() string {
	return filepath.Join(appdata.DataRootPath(), autoImportOffMarkerName)
}

func (manager *Manager) markerPath() string {
	if manager != nil && strings.TrimSpace(manager.importOffMarkerPath) != "" {
		return manager.importOffMarkerPath
	}
	return autoImportOffMarkerPath()
}

func writeAutoImportOffMarker(markerPath string) error {
	if strings.TrimSpace(markerPath) == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(markerPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(markerPath, []byte("1\n"), 0o600)
}

// ImportFromCursorBackup 从 cursor-byok 在注入模拟账号前备份的官方账号文件
// （cursor-auth-backup.json，字段 cursorAuth/accessToken 等）自动导入登录态，
// 免去用户在助手界面再次 PKCE 登录。仅在当前未登录且未标记「主动断开」时导入；
// 已登录（手动 PKCE 登录）时保持现状不覆盖。导入的 accessToken 若过期，
// Authorization 会用 refreshToken 自动刷新。
func (manager *Manager) ImportFromCursorBackup(backupPath string) (bool, error) {
	if manager == nil || strings.TrimSpace(backupPath) == "" {
		return false, nil
	}
	if _, err := os.Stat(manager.markerPath()); err == nil {
		return false, nil // 用户主动断开过官方账号，不再自动导入
	}
	data, err := os.ReadFile(backupPath)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var backup map[string]any
	if err := json.Unmarshal(data, &backup); err != nil {
		return false, fmt.Errorf("解析官方账号备份失败: %w", err)
	}
	accessToken, refreshToken, email := cursor.ReadCursorAuthBackupValues(backup)
	if strings.TrimSpace(accessToken) == "" {
		return false, nil
	}

	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.state == StateSignedIn && strings.TrimSpace(manager.credentials.AccessToken) != "" {
		return false, nil
	}
	// 取消进行中的 PKCE 登录：pollLogin 后续的 commit/finishWithError 因
	// loginGeneration 不匹配而成为 no-op，避免导入结果被旧登录流程覆盖。
	if manager.loginCancel != nil {
		manager.loginCancel()
		manager.loginCancel = nil
	}
	manager.loginGeneration++
	creds := credentials{
		AccessToken:  strings.TrimSpace(accessToken),
		RefreshToken: strings.TrimSpace(refreshToken),
		Email:        strings.TrimSpace(email),
	}
	if err := manager.save(creds); err != nil {
		manager.state = StateError
		manager.lastError = fmt.Sprintf("保存自动导入的 Cursor 凭据失败: %v", err)
		return false, err
	}
	manager.credentials = creds
	manager.state = StateSignedIn
	manager.lastError = ""
	return true, nil
}

func (manager *Manager) save(value credentials) error {
	manager.saveMu.Lock()
	defer manager.saveMu.Unlock()
	if manager.store == nil {
		return fmt.Errorf("cursor account store is not initialized")
	}
	summary, err := manager.store.Upsert(value)
	if err != nil {
		return err
	}
	_, err = manager.store.SetCurrent(summary.ID)
	return err
}

func (manager *Manager) openLoginPage(loginURL string) error {
	if manager.openLoginURL != nil {
		return manager.openLoginURL(loginURL)
	}
	return browser.OpenURL(loginURL)
}

func (manager *Manager) readLocalCursorAuth() (credentials, error) {
	if manager.localAuthReader != nil {
		return manager.localAuthReader()
	}
	path, err := cursor.CursorStateDBPath()
	if err != nil {
		return credentials{}, wrapAccountError("account_import_local_state_missing", "local cursor auth state is missing", err)
	}
	values, err := cursor.ReadCursorAuth(path)
	if err != nil {
		return credentials{}, wrapAccountError("account_import_local_state_missing", "local cursor auth state is missing", err)
	}
	if strings.TrimSpace(values.AccessToken) == "" {
		return credentials{}, accountError("account_import_local_state_missing", "local cursor auth state is missing")
	}
	return credentials{
		AccessToken:  strings.TrimSpace(values.AccessToken),
		RefreshToken: strings.TrimSpace(values.RefreshToken),
		Email:        strings.TrimSpace(values.Email),
	}, nil
}

func (manager *Manager) adoptImported(account importedAccount) (CursorAccountSummary, error) {
	tags, err := normalizeTags(account.Tags)
	if err != nil {
		return CursorAccountSummary{}, err
	}
	summary, err := manager.store.Upsert(credentials{
		AccessToken:  account.AccessToken,
		RefreshToken: account.RefreshToken,
		AuthID:       account.AuthID,
		Email:        account.Email,
	})
	if err != nil {
		return CursorAccountSummary{}, err
	}
	if len(tags) > 0 {
		summary, err = manager.store.UpdateTags(summary.ID, tags)
		if err != nil {
			return CursorAccountSummary{}, err
		}
	}
	if strings.TrimSpace(manager.store.CurrentAccountID()) == "" {
		summary, err = manager.store.SetCurrent(summary.ID)
		if err != nil {
			return CursorAccountSummary{}, err
		}
		manager.adoptCurrentFromStore()
	}
	return summary, nil
}

func (manager *Manager) adoptCurrentFromStore() {
	if manager == nil || manager.store == nil {
		return
	}
	creds, _, err := manager.store.LoadCurrentCredentials()
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if err != nil || strings.TrimSpace(creds.AccessToken) == "" {
		manager.credentials = credentials{}
		if manager.state != StateWaiting {
			manager.state = StateSignedOut
		}
		return
	}
	manager.credentials = creds
	if manager.state != StateWaiting {
		manager.state = StateSignedIn
		manager.lastError = ""
	}
}

func (manager *Manager) syncMemoryCredentials(creds credentials, generation uint64) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.loginGeneration != generation {
		return
	}
	manager.credentials = creds
	if manager.state != StateWaiting && strings.TrimSpace(creds.AccessToken) != "" {
		manager.state = StateSignedIn
		manager.lastError = ""
	}
}

func (manager *Manager) commitOAuthCredentials(generation uint64, value credentials) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.loginGeneration != generation {
		return ErrNotSignedIn
	}
	previous := manager.credentials
	summary, err := manager.store.Upsert(value)
	if err != nil {
		manager.state = StateError
		manager.lastError = fmt.Sprintf("保存 Cursor 登录凭据失败: %v", err)
		return err
	}
	if strings.TrimSpace(manager.store.CurrentAccountID()) == "" {
		if _, err := manager.store.SetCurrent(summary.ID); err != nil {
			manager.state = StateError
			manager.lastError = fmt.Sprintf("保存 Cursor 登录凭据失败: %v", err)
			return err
		}
		manager.credentials = value
	} else {
		manager.credentials = previous
	}
	if markerErr := os.Remove(manager.markerPath()); markerErr != nil && !errors.Is(markerErr, os.ErrNotExist) {
		logger.Errorf("removeAutoImportOffMarker failed: %v", markerErr)
	}
	if strings.TrimSpace(manager.credentials.AccessToken) != "" {
		manager.state = StateSignedIn
		manager.lastError = ""
	}
	return nil
}

func (manager *Manager) snapshotCredentials() (credentials, uint64) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.credentials, manager.loginGeneration
}

func (manager *Manager) finishWithError(generation uint64, message string) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.loginGeneration != generation {
		return
	}
	manager.lastError = strings.TrimSpace(message)
	if strings.TrimSpace(manager.credentials.AccessToken) != "" {
		manager.state = StateSignedIn
		return
	}
	manager.state = StateError
}

func (manager *Manager) commitCredentials(generation uint64, value credentials) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.loginGeneration != generation {
		return ErrNotSignedIn
	}
	if err := manager.save(value); err != nil {
		manager.state = StateError
		manager.lastError = fmt.Sprintf("保存 Cursor 登录凭据失败: %v", err)
		return err
	}
	// 手动 PKCE 登录成功，清除「主动断开」标记，恢复自动导入。
	if markerErr := os.Remove(manager.markerPath()); markerErr != nil && !errors.Is(markerErr, os.ErrNotExist) {
		logger.Errorf("removeAutoImportOffMarker failed: %v", markerErr)
	}
	manager.credentials = value
	manager.state = StateSignedIn
	manager.lastError = ""
	return nil
}

func (manager *Manager) setAuthorizationError(generation uint64, message string) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.loginGeneration != generation {
		return
	}
	manager.state = StateError
	manager.lastError = strings.TrimSpace(message)
}

func (manager *Manager) invalidateAuthorization(generation uint64, message string) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.loginGeneration != generation {
		return
	}
	manager.loginGeneration++
	manager.credentials = credentials{}
	manager.state = StateError
	manager.lastError = strings.TrimSpace(message)
	if manager.store != nil {
		_ = manager.store.clearCurrent()
	}
}

func buildLoginURL(loginID string, challenge string) (string, error) {
	parsed, err := url.Parse(websiteURL + "/loginDeepControl")
	if err != nil {
		return "", err
	}
	query := parsed.Query()
	query.Set("challenge", challenge)
	query.Set("uuid", loginID)
	query.Set("mode", "login")
	query.Set("supportsSelectedTeamLogin", "true")
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func bearer(token string) string {
	value := strings.TrimSpace(token)
	if strings.HasPrefix(strings.ToLower(value), "bearer ") {
		return value
	}
	return "Bearer " + value
}

func tokenNeedsRefresh(token string, now time.Time) bool {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) < 2 {
		return false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return false
	}
	claims := struct {
		ExpiresAt json.Number `json:"exp"`
	}{}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	if err := decoder.Decode(&claims); err != nil || claims.ExpiresAt == "" {
		return false
	}
	expiresAt, err := claims.ExpiresAt.Int64()
	if err != nil {
		return false
	}
	return !now.Add(refreshMargin).Before(time.Unix(expiresAt, 0))
}

func isRetryablePollError(err error) bool {
	if err == nil {
		return false
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "http 429") || strings.Contains(message, "http 5")
}
