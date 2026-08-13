package forwarder

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"cursor/internal/logger"
	"cursor/internal/safego"
)

const historyMaintenanceLockStaleAfter = 30 * time.Minute
const historyMaintenanceTempStaleAfter = 5 * time.Minute

// historyReconcileWriteThrottle 是启动期对账每写一条会话后的节流间隔。
const historyReconcileWriteThrottle = 5 * time.Millisecond

func (service *Service) startHistoryMaintenance() {
	if service == nil || service.store == nil {
		return
	}
	// 新建服务最常见的测试/首次启动场景里，history 根目录尚为空。此时没有任何
	// 启动期遗留物需要维护；若仍启动 goroutine，它可能晚于服务所有者退出才创建
	// .history-maintenance.lock，导致临时目录清理竞态，也让生产首次启动做无用工作。
	historyRoot := strings.TrimSpace(service.store.HistoryDir())
	entries, err := os.ReadDir(historyRoot)
	if errors.Is(err, os.ErrNotExist) || (err == nil && len(entries) == 0) {
		return
	}
	safego.Go("forwarder:history-maintenance", func() {
		if err := service.runHistoryMaintenance(); err != nil {
			logger.Errorf("forwarder history maintenance failed: %v", err)
		}
	})
}

func (service *Service) runHistoryMaintenance() error {
	historyRoot := strings.TrimSpace(service.store.HistoryDir())
	if historyRoot == "" {
		return nil
	}
	release, ok, err := acquireHistoryMaintenanceLock(historyRoot)
	if err != nil || !ok {
		return err
	}
	defer release()

	entries, err := os.ReadDir(historyRoot)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			cleanupRootLegacyHistoryArtifact(historyRoot, entry.Name())
			continue
		}
		service.cleanupConversationLegacyArtifacts(filepath.Join(historyRoot, entry.Name()))
		// 上次进程遗留的非终态会话在这里收口。复用本 pass 的原因：它已经是后台
		// goroutine、已持有 history 维护文件锁、已经在遍历 history 根目录；另起
		// 一个入口还要自己解决 newService 在启动链路里被构造多次的重复扫描问题。
		if service.reconcileStaleConversationLoop(entry.Name()) {
			// 存量遗留会话可能有几百条、几十 MB，给磁盘留出喘息，避免启动瞬间打满 I/O。
			time.Sleep(historyReconcileWriteThrottle)
		}
	}
	return nil
}

func cleanupRootLegacyHistoryArtifact(historyRoot string, name string) {
	trimmedName := strings.TrimSpace(name)
	if trimmedName == "" || trimmedName == ".history-maintenance.lock" {
		return
	}
	if !isRootLegacyHistoryArtifact(trimmedName) {
		return
	}
	path := filepath.Join(historyRoot, trimmedName)
	if strings.Contains(trimmedName, ".tmp-") {
		cleanupStaleHistoryTempArtifact(path)
		return
	}
	if err := os.RemoveAll(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		logger.Errorf("forwarder root legacy cleanup failed path=%s err=%v", path, err)
	}
}

func isRootLegacyHistoryArtifact(name string) bool {
	if strings.TrimSpace(name) == usageFileName || strings.TrimSpace(name) == usageFileName+".lock" {
		return false
	}
	if strings.HasSuffix(name, ".json") || strings.HasSuffix(name, ".jsonl") {
		return true
	}
	if strings.Contains(name, ".tmp-") {
		return true
	}
	if strings.HasSuffix(name, ".lock") {
		return true
	}
	return false
}

func (service *Service) cleanupConversationLegacyArtifacts(conversationDir string) {
	for _, name := range []string{
		"turns",
		"active",
		"latest.json",
		"summary.json",
		"replay.json",
		"runtime.json",
		"request.json",
		"recovery.json",
		"conversation.json",
		"entries.jsonl",
	} {
		path := filepath.Join(conversationDir, name)
		if err := os.RemoveAll(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			logger.Errorf("forwarder legacy cleanup failed path=%s err=%v", path, err)
		}
	}
	entries, err := os.ReadDir(conversationDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			if strings.Contains(entry.Name(), ".tmp-") {
				cleanupStaleHistoryTempArtifact(filepath.Join(conversationDir, entry.Name()))
			}
			continue
		}
		if _, err := strconv.Atoi(strings.TrimSpace(entry.Name())); err != nil {
			continue
		}
		path := filepath.Join(conversationDir, entry.Name())
		if err := os.RemoveAll(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			logger.Errorf("forwarder numeric legacy cleanup failed path=%s err=%v", path, err)
		}
	}
}

func cleanupStaleHistoryTempArtifact(path string) {
	info, err := os.Stat(path)
	if err != nil {
		return
	}
	if time.Since(info.ModTime()) < historyMaintenanceTempStaleAfter {
		return
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		logger.Errorf("forwarder temp cleanup failed path=%s err=%v", path, err)
	}
}

func acquireHistoryMaintenanceLock(historyRoot string) (func(), bool, error) {
	if err := os.MkdirAll(historyRoot, 0o755); err != nil {
		return nil, false, err
	}
	lockPath := filepath.Join(historyRoot, ".history-maintenance.lock")
	for attempt := 0; attempt < 2; attempt++ {
		file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_, _ = file.WriteString(time.Now().UTC().Format(time.RFC3339Nano))
			_ = file.Close()
			return func() {
				_ = os.Remove(lockPath)
			}, true, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, false, err
		}
		info, statErr := os.Stat(lockPath)
		if statErr != nil {
			if errors.Is(statErr, os.ErrNotExist) {
				continue
			}
			return nil, false, statErr
		}
		if time.Since(info.ModTime()) > historyMaintenanceLockStaleAfter {
			_ = os.Remove(lockPath)
			continue
		}
		return nil, false, nil
	}
	return nil, false, nil
}
