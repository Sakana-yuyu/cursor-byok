package cursor

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	cursorAuthAccessKey       = "cursorAuth/accessToken"
	cursorAuthRefreshKey      = "cursorAuth/refreshToken"
	cursorAuthEmailKey        = "cursorAuth/cachedEmail"
	cursorAuthSignUpKey       = "cursorAuth/cachedSignUpType"
	cursorAuthMembershipKey   = "cursorAuth/stripeMembershipType"
	cursorAuthSubscriptionKey = "cursorAuth/stripeSubscriptionStatus"
)

// CursorStateBackupFile is a non-secret backup inventory entry.
type CursorStateBackupFile struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// CursorStateBackup locates a file-level restore pack for state.vscdb and WAL/SHM.
type CursorStateBackup struct {
	DBPath      string
	Destination string
	Files       []CursorStateBackupFile
}

type cursorStateBackupManifest struct {
	Files []CursorStateBackupFile `json:"files"`
}

// ReplaceCursorAuth upserts required auth whitelist keys and optional non-empty
// fields inside one SQLite transaction. Empty optional fields delete the old
// key. Unrelated ItemTable rows are not scanned or copied.
func ReplaceCursorAuth(path string, values CursorAuthValues) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("cursor state db path is empty")
	}
	access := strings.TrimSpace(values.AccessToken)
	email := strings.TrimSpace(values.Email)
	if access == "" || email == "" {
		return fmt.Errorf("cursor auth replacement requires access token and email")
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return fmt.Errorf("open cursor state db: %w", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin cursor auth replacement: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if err := upsertCursorAuthKey(tx, cursorAuthAccessKey, access); err != nil {
		return err
	}
	if err := upsertCursorAuthKey(tx, cursorAuthEmailKey, email); err != nil {
		return err
	}
	if err := applyOptionalCursorAuthKey(tx, cursorAuthRefreshKey, values.RefreshToken); err != nil {
		return err
	}
	if err := applyOptionalCursorAuthKey(tx, cursorAuthSignUpKey, values.SignUpType); err != nil {
		return err
	}
	if err := applyOptionalCursorAuthKey(tx, cursorAuthMembershipKey, values.MembershipType); err != nil {
		return err
	}
	if err := applyOptionalCursorAuthKey(tx, cursorAuthSubscriptionKey, values.SubscriptionState); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit cursor auth replacement: %w", err)
	}
	committed = true
	return nil
}

func upsertCursorAuthKey(tx *sql.Tx, key, value string) error {
	_, err := tx.Exec("INSERT OR REPLACE INTO ItemTable(key, value) VALUES(?, ?)", key, value)
	if err != nil {
		return fmt.Errorf("upsert cursor auth key: %w", err)
	}
	return nil
}

func applyOptionalCursorAuthKey(tx *sql.Tx, key, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		if _, err := tx.Exec("DELETE FROM ItemTable WHERE key = ?", key); err != nil {
			return fmt.Errorf("delete empty cursor auth key: %w", err)
		}
		return nil
	}
	return upsertCursorAuthKey(tx, key, value)
}

// BackupCursorStateFiles copies state.vscdb and existing -wal/-shm sidecars.
func BackupCursorStateFiles(dbPath, destination string) (CursorStateBackup, error) {
	dbPath = strings.TrimSpace(dbPath)
	destination = strings.TrimSpace(destination)
	if dbPath == "" || destination == "" {
		return CursorStateBackup{}, fmt.Errorf("cursor state backup path is empty")
	}
	if err := os.MkdirAll(destination, 0o700); err != nil {
		return CursorStateBackup{}, err
	}
	backup := CursorStateBackup{DBPath: dbPath, Destination: destination, Files: []CursorStateBackupFile{}}
	for _, name := range []string{filepath.Base(dbPath), filepath.Base(dbPath) + "-wal", filepath.Base(dbPath) + "-shm"} {
		src := filepath.Join(filepath.Dir(dbPath), name)
		info, err := os.Stat(src)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return CursorStateBackup{}, err
		}
		if info.IsDir() {
			continue
		}
		dest := filepath.Join(destination, name)
		sum, size, err := copyFileSHA256(src, dest)
		if err != nil {
			return CursorStateBackup{}, err
		}
		backup.Files = append(backup.Files, CursorStateBackupFile{Name: name, Size: size, SHA256: sum})
	}
	if len(backup.Files) == 0 {
		return CursorStateBackup{}, fmt.Errorf("cursor state db is missing")
	}
	manifest := cursorStateBackupManifest{Files: backup.Files}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return CursorStateBackup{}, err
	}
	if err := os.WriteFile(filepath.Join(destination, "manifest.json"), append(data, '\n'), 0o600); err != nil {
		return CursorStateBackup{}, err
	}
	return backup, nil
}

// RestoreCursorStateFiles copies the backup files back beside the original DB.
func RestoreCursorStateFiles(backup CursorStateBackup) error {
	if strings.TrimSpace(backup.DBPath) == "" || strings.TrimSpace(backup.Destination) == "" {
		return fmt.Errorf("cursor state backup is incomplete")
	}
	restored := map[string]struct{}{}
	for _, file := range backup.Files {
		name := filepath.Base(file.Name)
		if name == "." || name == string(filepath.Separator) {
			return fmt.Errorf("invalid backup file name")
		}
		src := filepath.Join(backup.Destination, name)
		dest := filepath.Join(filepath.Dir(backup.DBPath), name)
		if _, _, err := copyFileSHA256(src, dest); err != nil {
			return err
		}
		restored[name] = struct{}{}
	}
	base := filepath.Base(backup.DBPath)
	for _, extra := range []string{base + "-wal", base + "-shm"} {
		if _, ok := restored[extra]; ok {
			continue
		}
		_ = os.Remove(filepath.Join(filepath.Dir(backup.DBPath), extra))
	}
	return nil
}

func copyFileSHA256(src, dest string) (string, int64, error) {
	in, err := os.Open(src)
	if err != nil {
		return "", 0, err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return "", 0, err
	}
	tmp := dest + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return "", 0, err
	}
	hasher := sha256.New()
	size, err := io.Copy(io.MultiWriter(out, hasher), in)
	closeErr := out.Close()
	if err != nil {
		_ = os.Remove(tmp)
		return "", 0, err
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return "", 0, closeErr
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		_ = os.Remove(tmp)
		return "", 0, err
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return "", 0, err
	}
	return hex.EncodeToString(hasher.Sum(nil)), size, nil
}
