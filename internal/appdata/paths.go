package appdata

import (
	"os"
	"path/filepath"
	"strings"
)

const (
	appDirName       = ".cursor-local-assistant-v2"
	legacyAppDirName = ".cursor-local-assistant"

	// RootDirEnvVar 用于把应用数据根目录整体重定向到别处。
	RootDirEnvVar = "CURSOR_BYOK_HOME"
)

// RootDir 返回应用配置根目录。
func RootDir() string {
	return appRootDir(appDirName)
}

func legacyRootDir() string {
	return appRootDir(legacyAppDirName)
}

func appRootDir(dirName string) string {
	homeDir := homeDirForAppData()
	if homeDir == "" {
		return dirName
	}
	return filepath.Join(homeDir, dirName)
}

// homeDirForAppData 返回推导应用数据目录时使用的主目录。
// RootDirEnvVar 优先于操作系统主目录，便于便携部署、多实例并存以及测试隔离。
func homeDirForAppData() string {
	if override := strings.TrimSpace(os.Getenv(RootDirEnvVar)); override != "" {
		return override
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return sandboxHomeIfUnderTest(strings.TrimSpace(homeDir))
}

// ConfigFilePath 返回统一用户配置文件路径。
func ConfigFilePath() string {
	return filepath.Join(RootDir(), "config.yaml")
}

// AVExclusionStateFilePath 返回「杀软排除项引导状态」持久化路径（记录是否已提示/已完成，
// 实现「仅提示一次」）。不进 config.yaml，避免污染用户可编辑配置。
func AVExclusionStateFilePath() string {
	return filepath.Join(RootDir(), "av-exclusion-state.json")
}

func DataRootPath() string {
	return filepath.Join(RootDir(), "data")
}

// ModelAdapterTestResultsFilePath 返回模型测速/可用性结果快照路径（进程外持久化，不进 config.yaml）。
func ModelAdapterTestResultsFilePath() string {
	return filepath.Join(DataRootPath(), "model-adapter-test-results.json")
}

func HistoryRootPath() string {
	return filepath.Join(RootDir(), "history")
}

func UsageFilePath() string {
	return filepath.Join(HistoryRootPath(), "usage.json")
}

func AdsRootPath() string {
	return filepath.Join(DataRootPath(), "ads")
}

func CodebaseIndexRootPath() string {
	return filepath.Join(DataRootPath(), "codebase-index")
}

func DocsIndexRootPath() string {
	return filepath.Join(DataRootPath(), "docs-index")
}

func RulesRootPath() string {
	return filepath.Join(RootDir(), "rules")
}

// SkillsRootPath 返回全局技能目录路径（每个子目录为一个技能，含 SKILL.md）。
// 该目录用于向每个 agent 对话的系统 prompt 注入全局技能。
func SkillsRootPath() string {
	return filepath.Join(RootDir(), "skills")
}

// LogsRootPath 返回统一日志根目录路径。
func LogsRootPath() string {
	return filepath.Join(RootDir(), "logs")
}

// CACertFilePath 返回注入给宿主的 CA 文件路径。
func CACertFilePath() string {
	return filepath.Join(DataRootPath(), "ca.crt")
}

// CAKeyFilePath 返回仅保存在用户数据目录中的 CA 私钥路径。
func CAKeyFilePath() string {
	return filepath.Join(DataRootPath(), "ca.key")
}
