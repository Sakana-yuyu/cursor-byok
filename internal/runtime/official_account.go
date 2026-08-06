package runtime

import (
	"encoding/json"
	"os"
	"path/filepath"

	"cursor/internal/appdata"
)

// OfficialAccountCredentials 表示 cursor-account.json 中的官方账号凭据。
// Hybrid 模式下客户端与 mock 账号信息应使用真实官方账号（而非本地模拟账号
// cursor@ai.com），使客户端会话特征与透传官方请求的 token 一致，官方才接受。
type OfficialAccountCredentials struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken,omitempty"`
	Email        string `json:"email"`
	AuthID       string `json:"authId,omitempty"`
}

// OfficialAccountPath 返回官方账号凭据文件路径（cursor-account.json）。
func OfficialAccountPath() string {
	return filepath.Join(appdata.DataRootPath(), "cursor-account.json")
}

// ReadOfficialAccountCredentials 读取官方账号凭据（cursor-account.json）。
// 未登录、文件缺失或字段不完整时返回 ok=false（调用方回退本地模拟账号）。
func ReadOfficialAccountCredentials() (OfficialAccountCredentials, bool) {
	data, err := os.ReadFile(OfficialAccountPath())
	if err != nil {
		return OfficialAccountCredentials{}, false
	}
	var creds OfficialAccountCredentials
	if err := json.Unmarshal(data, &creds); err != nil {
		return OfficialAccountCredentials{}, false
	}
	if creds.AccessToken == "" || creds.Email == "" {
		return OfficialAccountCredentials{}, false
	}
	return creds, true
}
