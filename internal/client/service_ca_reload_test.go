package client

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"cursor/internal/appdata"
	serverconfig "cursor/internal/backend/server/config"
	"cursor/internal/certs"
	"cursor/internal/logger"
	"cursor/internal/mitm"
)

// A real CONNECT + TLS handshake exercises the proxy's captured signing key and
// certificate cache, rather than only checking the service's manager pointer.
func caReloadHandshake(t *testing.T, proxy *mitm.ProxyServer, manager *certs.Manager) *x509.Certificate {
	t.Helper()
	conn, err := net.DialTimeout("tcp", proxy.Snapshot().ListenAddr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	const host = "api2.cursor.sh"
	if _, err := fmt.Fprintf(conn, "CONNECT %s:443 HTTP/1.1\r\nHost: %s:443\r\n\r\n", host, host); err != nil {
		t.Fatal(err)
	}
	response, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: http.MethodConnect})
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("CONNECT status = %s", response.Status)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(manager.CACertPEM()) {
		t.Fatal("invalid test CA")
	}
	tlsConn := tls.Client(conn, &tls.Config{RootCAs: roots, ServerName: host, MinVersion: tls.VersionTLS12})
	defer tlsConn.Close()
	if err := tlsConn.Handshake(); err != nil {
		t.Fatalf("proxy TLS handshake using expected CA: %v", err)
	}
	state := tlsConn.ConnectionState()
	if len(state.VerifiedChains) == 0 || len(state.PeerCertificates) == 0 {
		t.Fatal("TLS handshake did not verify a certificate chain")
	}
	return state.PeerCertificates[0]
}

func caReloadStop(t *testing.T, proxy *mitm.ProxyServer) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := proxy.Stop(ctx); err != nil {
		t.Fatalf("stop proxy: %v", err)
	}
}

func caReloadFixture(t *testing.T, degraded bool) (*ProxyService, *certs.Manager) {
	t.Helper()
	// The logger owns a process-wide Windows file handle; initialize it before
	// redirecting CA storage so it cannot prevent this test's TempDir cleanup.
	logger.Init()
	t.Setenv(appdata.RootDirEnvVar, t.TempDir())
	manager, err := certs.NewPersistentManager(appdata.CACertFilePath(), appdata.CAKeyFilePath())
	if err != nil {
		t.Fatal(err)
	}
	proxyManager := manager
	if degraded {
		proxyManager = nil
	}
	proxy, err := mitm.NewProxyServer("127.0.0.1:0", "http://127.0.0.1:1", "", nil, proxyManager)
	if err != nil {
		t.Fatal(err)
	}
	s := &ProxyService{proxy: proxy, certManager: proxyManager, caCertPEM: manager.CACertPEM()}
	t.Cleanup(func() {
		caReloadStop(t, proxy)
		if current := s.currentProxy(); current != nil && current != proxy {
			caReloadStop(t, current)
		}
	})
	return s, manager
}

func TestReloadCAFromDiskRebuildsStoppedProxyTLS(t *testing.T) {
	for _, degraded := range []bool{false, true} {
		t.Run(fmt.Sprintf("degraded=%t", degraded), func(t *testing.T) {
			s, oldManager := caReloadFixture(t, degraded)
			oldProxy := s.currentProxy()
			if !degraded {
				if err := oldProxy.Start(); err != nil {
					t.Fatal(err)
				}
				caReloadHandshake(t, oldProxy, oldManager)
				caReloadStop(t, oldProxy)
			}
			before := oldProxy.Snapshot()
			if err := os.Remove(appdata.CAKeyFilePath()); err != nil {
				t.Fatal(err)
			}
			s.MarkCAIncomplete(errors.New("test: missing CA key"))
			backup, err := s.ReloadCAFromDisk()
			if err != nil {
				t.Fatal(err)
			}
			if backup == "" {
				t.Fatal("repair did not back up the incomplete CA")
			}
			newManager := s.currentCertManager()
			if bytes.Equal(oldManager.CACertPEM(), newManager.CACertPEM()) {
				t.Fatal("repair kept the old CA")
			}
			proxy := s.currentProxy()
			if proxy == nil || proxy == oldProxy {
				t.Fatal("stopped proxy was not rebuilt")
			}
			after := proxy.Snapshot()
			if after.ListenAddr != before.ListenAddr || after.BaseURL != before.BaseURL || after.Running {
				t.Fatalf("rebuilt proxy changed configuration or started: before=%+v after=%+v", before, after)
			}
			// Reproduce the same-address fast path used by StartProxy.
			if err := s.ensureProxy(serverconfig.Config{ProxyListenAddr: before.ListenAddr, BackendListenAddr: "127.0.0.1:1"}); err != nil {
				t.Fatal(err)
			}
			if s.currentProxy() != proxy {
				t.Fatal("same-address ensure unexpectedly replaced rebuilt proxy")
			}
			if err := proxy.Start(); err != nil {
				t.Fatal(err)
			}
			leaf := caReloadHandshake(t, proxy, newManager)
			oldCA, err := oldManager.CATLSCertificate()
			if err != nil {
				t.Fatal(err)
			}
			if err := leaf.CheckSignatureFrom(oldCA.Leaf); err == nil {
				t.Fatal("new proxy leaf is still signed by old CA")
			}
			if s.caIncomplete || s.caError != "" || !bytes.Equal(s.caCertPEM, newManager.CACertPEM()) || s.caFilePath != appdata.CACertFilePath() {
				t.Fatal("repaired service CA state is inconsistent")
			}
		})
	}
}

func TestReloadCAFromDiskRejectsRunningProxyBeforeDiskChanges(t *testing.T) {
	for _, incomplete := range []bool{false, true} {
		t.Run(fmt.Sprintf("incomplete=%t", incomplete), func(t *testing.T) {
			s, manager := caReloadFixture(t, false)
			proxy := s.currentProxy()
			if err := proxy.Start(); err != nil {
				t.Fatal(err)
			}
			if incomplete {
				if err := os.Remove(appdata.CAKeyFilePath()); err != nil {
					t.Fatal(err)
				}
				s.MarkCAIncomplete(errors.New("test: missing CA key"))
			}
			before, err := os.ReadFile(appdata.CACertFilePath())
			if err != nil {
				t.Fatal(err)
			}
			backup, err := s.ReloadCAFromDisk()
			if err == nil || !strings.Contains(err.Error(), "请先停止服务") || backup != "" {
				t.Fatalf("running repair: backup=%q err=%v", backup, err)
			}
			if s.currentProxy() != proxy || s.currentCertManager() != manager || !proxy.IsRunning() || s.caIncomplete != incomplete {
				t.Fatal("rejected repair changed service state")
			}
			after, err := os.ReadFile(appdata.CACertFilePath())
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("rejected repair changed disk certificate: %v", err)
			}
			if incomplete {
				if _, err := os.Stat(appdata.CAKeyFilePath()); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("rejected repair recreated key: %v", err)
				}
			}
			caReloadHandshake(t, proxy, manager)
		})
	}
}

func TestReloadCAFromDiskCompleteCAPreservesIdentity(t *testing.T) {
	s, manager := caReloadFixture(t, false)
	keyBefore, err := os.ReadFile(appdata.CAKeyFilePath())
	if err != nil {
		t.Fatal(err)
	}
	backup, err := s.ReloadCAFromDisk()
	if err != nil || backup != "" {
		t.Fatalf("complete CA reload: backup=%q err=%v", backup, err)
	}
	keyAfter, err := os.ReadFile(appdata.CAKeyFilePath())
	if err != nil || !bytes.Equal(keyBefore, keyAfter) || !bytes.Equal(manager.CACertPEM(), s.currentCertManager().CACertPEM()) {
		t.Fatalf("complete CA identity changed: %v", err)
	}
	if err := s.currentProxy().Start(); err != nil {
		t.Fatal(err)
	}
	caReloadHandshake(t, s.currentProxy(), manager)
}

func TestReloadCAFromDiskWithoutExistingProxy(t *testing.T) {
	s, manager := caReloadFixture(t, false)
	s.mu.Lock()
	s.proxy = nil
	s.mu.Unlock()
	backup, err := s.ReloadCAFromDisk()
	if err != nil || backup != "" {
		t.Fatalf("reload without proxy: backup=%q err=%v", backup, err)
	}
	if s.currentProxy() != nil || !bytes.Equal(manager.CACertPEM(), s.currentCertManager().CACertPEM()) {
		t.Fatal("reload created an unnecessary proxy or changed complete CA")
	}
	if err := s.ensureProxy(serverconfig.Config{ProxyListenAddr: "127.0.0.1:0", BackendListenAddr: "127.0.0.1:1"}); err != nil {
		t.Fatal(err)
	}
	if err := s.currentProxy().Start(); err != nil {
		t.Fatal(err)
	}
	caReloadHandshake(t, s.currentProxy(), manager)
}

func TestReloadCAFromDiskFailurePreservesState(t *testing.T) {
	s, manager := caReloadFixture(t, false)
	proxy := s.currentProxy()
	s.MarkCAIncomplete(errors.New("test: unreadable CA"))
	// A directory at the certificate path produces a read error, rather than a
	// repairable missing file, on both Windows and Unix.
	if err := os.Remove(appdata.CACertFilePath()); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(appdata.CACertFilePath(), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReloadCAFromDisk(); err == nil {
		t.Fatal("unreadable CA unexpectedly reloaded")
	}
	if s.currentProxy() != proxy || s.currentCertManager() != manager || !s.caIncomplete || s.caError == "" {
		t.Fatal("failed reload published replacement CA state")
	}
}

func TestReloadCAFromDiskWaitsForLifecycle(t *testing.T) {
	s, _ := caReloadFixture(t, false)
	s.lifecycleMu.Lock()
	done := make(chan error, 1)
	started := make(chan struct{})
	go func() {
		close(started)
		_, err := s.ReloadCAFromDisk()
		done <- err
	}()
	<-started
	select {
	case err := <-done:
		s.lifecycleMu.Unlock()
		t.Fatalf("CA reload bypassed lifecycle lock: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	s.lifecycleMu.Unlock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("CA reload did not finish after lifecycle lock was released")
	}
}
