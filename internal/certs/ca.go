package certs

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"log"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Manager 定义了当前模块中的 Manager 类型。
type Manager struct {
	// caCert 表示当前声明中的 caCert。
	caCert *x509.Certificate
	// caKey 表示当前声明中的 caKey。
	caKey crypto.PrivateKey

	// mu 表示当前声明中的 mu。
	mu sync.Mutex
	// cache 表示当前声明中的 cache。
	cache map[string]*tls.Certificate
}

// NewManager 用于处理与 NewManager 相关的逻辑。
func NewManager(caCertPath, caKeyPath string) (*Manager, error) {
	certPEM, keyPEM, err := loadCAPEMFromFiles(caCertPath, caKeyPath)
	if err != nil {
		return nil, err
	}
	return NewManagerFromPEM(certPEM, keyPEM)
}

// IncompleteCAError 表示本地 CA 材料不完整（cert/key 仅存在其一）。
// 属于可恢复错误：应用降级启动后，用户可经「一键修复」重新生成 CA。
type IncompleteCAError struct {
	CertPath string
	KeyPath  string
	// CertExists 表示残留的是证书（true）还是私钥（false）。
	CertExists bool
}

func (e *IncompleteCAError) Error() string {
	if e == nil {
		return "incomplete CA material"
	}
	present := "CA 私钥(key)"
	missing := "CA 证书(cert)"
	if e.CertExists {
		present = "CA 证书(cert)"
		missing = "CA 私钥(key)"
	}
	return fmt.Sprintf(
		"检测到 CA 材料不完整（%s 存在、%s 缺失），为避免覆盖既有 CA 导致信任失效，"+
			"本地代理已停用，请在应用中执行「一键修复」重新生成：cert=%s key=%s",
		present, missing, e.CertPath, e.KeyPath,
	)
}

// IsIncompleteCA 判断错误是否为 CA 材料不完整（可修复）。
func IsIncompleteCA(err error) bool {
	var target *IncompleteCAError
	return errors.As(err, &target)
}

// NewPersistentManager loads an existing local CA or creates one on first use.
// The private key stays in the per-user data directory and is never part of the repository.
//
// 生成策略（确保「升级版本不重新生成 CA」）：
//   - cert 与 key 都存在：直接复用，绝不重新生成（升级、重启均走这条路径）。
//   - cert 与 key 都不存在：首次安装，生成新 CA 并落盘。
//   - 仅存在其一（例如 key 被误删）：返回 *IncompleteCAError，【不会】静默覆盖——
//     避免悄悄换一张新 CA 导致既有系统信任与 NODE_EXTRA_CA_CERTS 全部失效。
//     调用方应降级启动并引导用户执行 RepairIncompleteCA 后重启应用。
func NewPersistentManager(certPath, keyPath string) (*Manager, error) {
	certPath = filepath.Clean(strings.TrimSpace(certPath))
	keyPath = filepath.Clean(strings.TrimSpace(keyPath))
	if certPath == "." || keyPath == "." || certPath == "" || keyPath == "" {
		return nil, errors.New("CA paths are required")
	}
	certPEM, certErr := os.ReadFile(certPath)
	keyPEM, keyErr := os.ReadFile(keyPath)

	// 路径 1：两份都在 → 复用，绝不重新生成。
	if certErr == nil && keyErr == nil {
		// 复用前校验 cert/key 是否属于同一密钥对：loadCAFromPEM 只分别解析，
		// 不校验配对（tls 签名时才暴露）。若 ca.crt 曾被单独覆盖（如
		// EnsureCACertFile 用独立 PEM 覆盖写），会得到「cert/key 不匹配」，
		// 直接复用会导致 MITM 签名全挂、Cursor 一直 reconnecting。
		// 此时备份残留并重新生成一对匹配的 CA。
		if !certKeyMatch(certPEM, keyPEM) {
			backup, backupErr := backupCAFiles(certPath, keyPath)
			if backupErr != nil {
				return nil, backupErr
			}
			log.Printf("[certs] CA cert/key mismatch, regenerating backup=%s cert=%s key=%s", backup, certPath, keyPath)
			if writeErr := writeGeneratedCA(certPath, keyPath); writeErr != nil {
				return nil, writeErr
			}
			certPEM, keyPEM, readErr := readWrittenCAPEM(certPath, keyPath)
			if readErr != nil {
				return nil, readErr
			}
			return NewManagerFromPEM(certPEM, keyPEM)
		}
		log.Printf("[certs] reuse existing CA cert=%s key=%s (no regeneration)", certPath, keyPath)
		return NewManagerFromPEM(certPEM, keyPEM)
	}

	// 非「文件不存在」的读错误（权限等）直接上抛。
	if certErr != nil && !errors.Is(certErr, os.ErrNotExist) {
		return nil, certErr
	}
	if keyErr != nil && !errors.Is(keyErr, os.ErrNotExist) {
		return nil, keyErr
	}

	// 路径 2：只有一份存在 → 不静默覆盖，报错让用户知情并引导修复。
	certExists := certErr == nil
	keyExists := keyErr == nil
	if certExists != keyExists {
		return nil, &IncompleteCAError{
			CertPath:  certPath,
			KeyPath:   keyPath,
			CertExists: certExists,
		}
	}

	// 路径 3：两份都不存在 → 首次安装，生成新 CA。
	log.Printf("[certs] first run, generating new CA cert=%s key=%s", certPath, keyPath)
	if err := writeGeneratedCA(certPath, keyPath); err != nil {
		return nil, err
	}
	certPEM, keyPEM, err := readWrittenCAPEM(certPath, keyPath)
	if err != nil {
		return nil, err
	}
	return NewManagerFromPEM(certPEM, keyPEM)
}

// writeGeneratedCA 生成新 CA 并落盘（首次安装与修复共用）。
// 每个文件先写同目录临时文件再 rename（原子替换）；key 写入失败时回滚已落盘的 cert，
// 避免留下「cert/key 仅存其一」的不完整状态。
func writeGeneratedCA(certPath, keyPath string) error {
	certPEM, keyPEM, err := generateCAPEM()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(certPath), 0o700); err != nil {
		return err
	}
	if err := writePrivateFileAtomic(certPath, certPEM); err != nil {
		return err
	}
	if err := writePrivateFileAtomic(keyPath, keyPEM); err != nil {
		_ = os.Remove(certPath) // 回滚：避免留下只有 cert 的半截状态
		return err
	}
	return nil
}

// writePrivateFileAtomic 原子写入：同目录临时文件 + rename。
// 直接 os.WriteFile 若在写入中途被中断（进程崩溃/被杀软拦截）会留下半截文件，
// 这正是历史上「cert/key 仅存其一」存量状态的来源之一。
func writePrivateFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".ca-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) // rename 成功后为 no-op
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

// readWrittenCAPEM 读取刚写入的 CA 材料。生成成功但读取失败属于内部错误，上传具体
// 文件路径，避免静默吞错后让 NewManagerFromPEM 报出难以定位的解析错误。
func readWrittenCAPEM(certPath, keyPath string) ([]byte, []byte, error) {
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return nil, nil, fmt.Errorf("read generated CA cert: %w", err)
	}
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, nil, fmt.Errorf("read generated CA key: %w", err)
	}
	return certPEM, keyPEM, nil
}

// RepairIncompleteCA 修复 CA 材料不完整：把残留文件备份改名（.corrupt-<时间戳>.bak），
// 再重新生成 CA 落盘。仅在材料不完整（IsIncompleteCA）时调用；两份齐全时不做任何事。
// 注意：重新生成后旧 CA 的系统信任/NODE_EXTRA_CA_CERTS 指向会失效，调用方应引导
// 用户重新信任并重启应用。
func RepairIncompleteCA(certPath, keyPath string) (backupPath string, err error) {
	certPath = filepath.Clean(strings.TrimSpace(certPath))
	keyPath = filepath.Clean(strings.TrimSpace(keyPath))

	// 双份齐全：无需修复（不覆盖既有 CA）。
	if _, certErr := os.Stat(certPath); certErr == nil {
		if _, keyErr := os.Stat(keyPath); keyErr == nil {
			return "", nil
		}
	}

	// 备份残留文件（仅改名的文件，其他状态不动）。
	backup, err := backupCAFiles(certPath, keyPath)
	if err != nil {
		return "", err
	}
	log.Printf("[certs] incomplete CA repaired: backup=%s cert=%s key=%s", backup, certPath, keyPath)
	if err := writeGeneratedCA(certPath, keyPath); err != nil {
		return backup, err
	}
	return backup, nil
}

// CACertPEM returns a copy of the public CA certificate used by this manager.
func (m *Manager) CACertPEM() []byte {
	if m == nil || m.caCert == nil {
		return nil
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: m.caCert.Raw})
}

func generateCAPEM() ([]byte, []byte, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, err
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   "Cursor Local Proxy CA",
			Organization: []string{"Cursor Local Assistant"},
		},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            1,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	return certPEM, keyPEM, nil
}

// NewManagerFromPEM 用于处理与 NewManagerFromPEM 相关的逻辑。
func NewManagerFromPEM(caCertPEM, caKeyPEM []byte) (*Manager, error) {
	caCert, caKey, err := loadCAFromPEM(caCertPEM, caKeyPEM)
	if err != nil {
		return nil, err
	}
	return &Manager{caCert: caCert, caKey: caKey, cache: make(map[string]*tls.Certificate)}, nil
}

// CATLSCertificate 用于处理与 CATLSCertificate 相关的逻辑。
func (m *Manager) CATLSCertificate() (*tls.Certificate, error) {
	if m == nil || m.caCert == nil || m.caKey == nil {
		return nil, errors.New("CA is not initialized")
	}
	return &tls.Certificate{
		Certificate: [][]byte{append([]byte(nil), m.caCert.Raw...)},
		PrivateKey:  m.caKey,
		Leaf:        m.caCert,
	}, nil
}

// CertificateForServerName 用于处理与 CertificateForServerName 相关的逻辑。
func (m *Manager) CertificateForServerName(serverName string) (*tls.Certificate, error) {
	host := normalizeHost(serverName)
	if host == "" {
		return nil, errors.New("empty server name")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if cert, ok := m.cache[host]; ok {
		return cert, nil
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, err
	}

	leaf := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   host,
			Organization: []string{"Cursor Local Proxy"},
		},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	if len(m.caCert.SubjectKeyId) > 0 {
		leaf.AuthorityKeyId = append([]byte(nil), m.caCert.SubjectKeyId...)
	}

	if ip := net.ParseIP(host); ip != nil {
		leaf.IPAddresses = []net.IP{ip}
	} else {
		leaf.DNSNames = []string{host}
	}

	leafPrivateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	leafPublicKey := &leafPrivateKey.PublicKey

	der, err := x509.CreateCertificate(rand.Reader, leaf, m.caCert, leafPublicKey, m.caKey)
	if err != nil {
		return nil, err
	}

	leafCertPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	chainPEM := append([]byte(nil), leafCertPEM...)
	chainPEM = append(chainPEM, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: m.caCert.Raw})...)

	keyPEM, err := marshalPrivateKeyPEM(leafPrivateKey)
	if err != nil {
		return nil, err
	}

	pair, err := tls.X509KeyPair(chainPEM, keyPEM)
	if err != nil {
		return nil, err
	}
	parsedLeaf, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	pair.Leaf = parsedLeaf

	m.cache[host] = &pair
	return &pair, nil
}

// marshalPrivateKeyPEM 用于处理与 marshalPrivateKeyPEM 相关的逻辑。
func marshalPrivateKeyPEM(key any) ([]byte, error) {
	switch k := key.(type) {
	case *rsa.PrivateKey:
		return pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)}), nil
	case *ecdsa.PrivateKey:
		der, err := x509.MarshalECPrivateKey(k)
		if err != nil {
			return nil, err
		}
		return pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), nil
	case ed25519.PrivateKey:
		der, err := x509.MarshalPKCS8PrivateKey(k)
		if err != nil {
			return nil, err
		}
		return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), nil
	default:
		return nil, errors.New("unsupported private key type")
	}
}

// loadCAPEMFromFiles 用于处理与 loadCAPEMFromFiles 相关的逻辑。
func loadCAPEMFromFiles(certPath, keyPath string) ([]byte, []byte, error) {
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return nil, nil, err
	}
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, nil, err
	}
	return certPEM, keyPEM, nil
}

// loadCAFromPEM 用于处理与 loadCAFromPEM 相关的逻辑。
func loadCAFromPEM(certPEM, keyPEM []byte) (*x509.Certificate, crypto.PrivateKey, error) {
	certBlock, _ := pem.Decode(certPEM)
	if certBlock == nil {
		return nil, nil, errors.New("invalid CA cert PEM")
	}
	caCert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, nil, err
	}

	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		return nil, nil, errors.New("invalid CA key PEM")
	}

	switch keyBlock.Type {
	case "RSA PRIVATE KEY":
		key, err := x509.ParsePKCS1PrivateKey(keyBlock.Bytes)
		if err != nil {
			return nil, nil, err
		}
		return caCert, key, nil
	case "EC PRIVATE KEY":
		key, err := x509.ParseECPrivateKey(keyBlock.Bytes)
		if err != nil {
			return nil, nil, err
		}
		return caCert, key, nil
	case "PRIVATE KEY":
		key, err := x509.ParsePKCS8PrivateKey(keyBlock.Bytes)
		if err != nil {
			return nil, nil, err
		}
		return caCert, key, nil
	default:
		return nil, nil, errors.New("unsupported CA key format")
	}
}

// normalizeHost 用于处理与 normalizeHost 相关的逻辑。
func normalizeHost(serverName string) string {
	serverName = strings.TrimSpace(serverName)
	if strings.Contains(serverName, ":") {
		h, _, err := net.SplitHostPort(serverName)
		if err == nil {
			serverName = h
		}
	}
	return serverName
}

// cloneBytes 用于处理与 cloneBytes 相关的逻辑。
func cloneBytes(src []byte) []byte {
	dst := make([]byte, len(src))
	copy(dst, src)
	return dst
}

// certKeyMatch 校验 cert PEM 与 key PEM 是否属于同一密钥对。
// loadCAFromPEM 只分别解析 cert/key，不校验配对；tls 签名时才会暴露
// 「PrivateKey doesn't match parent's PublicKey」。在复用/加载前先做
// 公钥比对，避免 ca.crt 被单独覆盖后带病启动。
func certKeyMatch(certPEM, keyPEM []byte) bool {
	cert, key, err := loadCAFromPEM(certPEM, keyPEM)
	if err != nil {
		return false
	}
	certPub := cert.PublicKey
	switch keyImpl := key.(type) {
	case *rsa.PrivateKey:
		rsaCertPub, ok := certPub.(*rsa.PublicKey)
		if !ok {
			return false
		}
		return rsaCertPub.N.Cmp(keyImpl.PublicKey.N) == 0 && rsaCertPub.E == keyImpl.PublicKey.E
	case *ecdsa.PrivateKey:
		ecdsaCertPub, ok := certPub.(*ecdsa.PublicKey)
		if !ok {
			return false
		}
		return ecdsaCertPub.X.Cmp(keyImpl.PublicKey.X) == 0 && ecdsaCertPub.Y.Cmp(keyImpl.PublicKey.Y) == 0
	case ed25519.PrivateKey:
		edCertPub, ok := certPub.(ed25519.PublicKey)
		if !ok {
			return false
		}
		return bytes.Equal(edCertPub, keyImpl.Public().(ed25519.PublicKey))
	}
	return false
}

// backupCAFiles 把存在的 CA 材料改名备份（.corrupt-<时间戳>.bak），返回备份路径。
// 与 RepairIncompleteCA / NewPersistentManager 的不匹配重建共用。
func backupCAFiles(certPath, keyPath string) (string, error) {
	backup := ""
	for _, path := range []string{certPath, keyPath} {
		if _, statErr := os.Stat(path); statErr != nil {
			continue
		}
		renamed := fmt.Sprintf("%s.corrupt-%d.bak", path, time.Now().Unix())
		if err := os.Rename(path, renamed); err != nil {
			return "", fmt.Errorf("备份 CA 文件失败 %s: %w", path, err)
		}
		backup = renamed
	}
	return backup, nil
}
