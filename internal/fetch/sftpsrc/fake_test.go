package sftpsrc

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// memFile is one file served by fakeSFTP. A stalling file serves its first
// stallAfter bytes, then blocks reads until the server shuts down.
type memFile struct {
	data       []byte
	mtime      time.Time
	dir        bool
	denied     bool
	stallAfter int
}

// fakeSFTP is an in-process SSH server with an SFTP subsystem backed by an
// in-memory file map (pkg/sftp's request server).
type fakeSFTP struct {
	t       *testing.T
	ln      net.Listener
	hostKey ssh.Signer
	user    string
	pass    string
	userKey ssh.PublicKey // accepted client key
	files   map[string]memFile
	done    chan struct{}

	mu     sync.Mutex
	logins int
	conns  map[net.Conn]struct{}
	wg     sync.WaitGroup
}

func newSigner(t *testing.T) ssh.Signer {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	s, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// authorizedKey renders k in authorized_keys form.
func authorizedKey(k ssh.PublicKey) string {
	return string(bytes.TrimSpace(ssh.MarshalAuthorizedKey(k)))
}

// testdataFile reads a committed test key. id_ed25519 is the client key
// (id_ed25519_enc is the same key encrypted with passphrase "pw" and one
// KDF round, so tests stay fast); ssh_host_ed25519_key is the compose sftp
// service's host key.
func testdataFile(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// clientPublicKey is the public half of testdata/id_ed25519.
func clientPublicKey(t *testing.T) ssh.PublicKey {
	t.Helper()
	k, _, _, _, err := ssh.ParseAuthorizedKey([]byte(testdataFile(t, "id_ed25519.pub")))
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func newFakeSFTP(t *testing.T, files map[string]memFile, userKey ssh.PublicKey) *fakeSFTP {
	t.Helper()
	hostKey := newSigner(t)
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeSFTP{
		t: t, ln: ln, hostKey: hostKey, user: "xi", pass: "secret", userKey: userKey,
		files: files, done: make(chan struct{}), conns: map[net.Conn]struct{}{},
	}
	f.wg.Go(f.accept)
	t.Cleanup(func() {
		close(f.done)
		_ = ln.Close()
		f.dropAll()
		f.wg.Wait()
	})
	return f
}

// config returns a password Config pinned to the server's host key.
func (f *fakeSFTP) config() Config {
	host, port, _ := net.SplitHostPort(f.ln.Addr().String())
	p, _ := strconv.Atoi(port)
	return Config{
		Name: "sftp-test", Host: host, Port: p, User: f.user, Password: f.pass,
		HostKey: authorizedKey(f.hostKey.PublicKey()), PoolSize: 1,
	}
}

func (f *fakeSFTP) loginCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.logins
}

func (f *fakeSFTP) dropAll() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for c := range f.conns {
		_ = c.Close()
	}
}

func (f *fakeSFTP) login() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.logins++
}

func (f *fakeSFTP) accept() {
	cfg := &ssh.ServerConfig{
		PasswordCallback: func(c ssh.ConnMetadata, pw []byte) (*ssh.Permissions, error) {
			if c.User() == f.user && string(pw) == f.pass {
				f.login()
				return nil, nil
			}
			return nil, errors.New("denied")
		},
		PublicKeyCallback: func(c ssh.ConnMetadata, k ssh.PublicKey) (*ssh.Permissions, error) {
			if f.userKey != nil && c.User() == f.user && bytes.Equal(k.Marshal(), f.userKey.Marshal()) {
				f.login()
				return nil, nil
			}
			return nil, errors.New("denied")
		},
	}
	cfg.AddHostKey(f.hostKey)
	for {
		nc, err := f.ln.Accept()
		if err != nil {
			return
		}
		f.mu.Lock()
		f.conns[nc] = struct{}{}
		f.mu.Unlock()
		f.wg.Go(func() {
			defer func() {
				f.mu.Lock()
				delete(f.conns, nc)
				f.mu.Unlock()
				_ = nc.Close()
			}()
			f.serve(nc, cfg)
		})
	}
}

func (f *fakeSFTP) serve(nc net.Conn, cfg *ssh.ServerConfig) {
	sc, chans, reqs, err := ssh.NewServerConn(nc, cfg)
	if err != nil {
		return
	}
	defer sc.Close()
	f.wg.Go(func() { ssh.DiscardRequests(reqs) })
	for nch := range chans {
		if nch.ChannelType() != "session" {
			_ = nch.Reject(ssh.UnknownChannelType, "session only")
			continue
		}
		ch, creqs, err := nch.Accept()
		if err != nil {
			return
		}
		f.wg.Go(func() {
			for req := range creqs {
				ok := req.Type == "subsystem" && len(req.Payload) > 4 && string(req.Payload[4:]) == "sftp"
				_ = req.Reply(ok, nil)
				if ok {
					f.wg.Go(func() {
						rs := sftp.NewRequestServer(ch, sftp.Handlers{FileGet: f, FilePut: f, FileCmd: f, FileList: f})
						_ = rs.Serve()
						_ = rs.Close()
					})
				}
			}
		})
	}
}

func (f *fakeSFTP) lookup(p string) (memFile, error) {
	file, ok := f.files[p]
	switch {
	case !ok:
		return memFile{}, os.ErrNotExist
	case file.denied:
		return memFile{}, sftp.ErrSSHFxPermissionDenied
	default:
		return file, nil
	}
}

// Fileread implements sftp.FileReader.
func (f *fakeSFTP) Fileread(r *sftp.Request) (io.ReaderAt, error) {
	file, err := f.lookup(r.Filepath)
	if err != nil {
		return nil, err
	}
	return &memReader{file: file, done: f.done}, nil
}

// Filewrite implements sftp.FileWriter (read-only server).
func (f *fakeSFTP) Filewrite(*sftp.Request) (io.WriterAt, error) { return nil, os.ErrPermission }

// Filecmd implements sftp.FileCmder (read-only server).
func (f *fakeSFTP) Filecmd(*sftp.Request) error { return os.ErrPermission }

// Filelist implements sftp.FileLister for Stat.
func (f *fakeSFTP) Filelist(r *sftp.Request) (sftp.ListerAt, error) {
	if r.Method != "Stat" && r.Method != "Lstat" {
		return nil, os.ErrPermission
	}
	file, ok := f.files[r.Filepath]
	if !ok {
		return nil, os.ErrNotExist
	}
	return lister{&memInfo{name: r.Filepath, f: file}}, nil
}

type memReader struct {
	file memFile
	done chan struct{}
}

func (m *memReader) ReadAt(p []byte, off int64) (int, error) {
	data := m.file.data
	if m.file.stallAfter > 0 {
		if off >= int64(m.file.stallAfter) {
			<-m.done
			return 0, io.ErrUnexpectedEOF
		}
		data = data[:m.file.stallAfter]
	}
	if off >= int64(len(data)) {
		return 0, io.EOF
	}
	n := copy(p, data[off:])
	if n < len(p) && m.file.stallAfter == 0 {
		return n, io.EOF
	}
	return n, nil
}

type lister []os.FileInfo

func (l lister) ListAt(dst []os.FileInfo, off int64) (int, error) {
	if off >= int64(len(l)) {
		return 0, io.EOF
	}
	n := copy(dst, l[off:])
	return n, nil
}

type memInfo struct {
	name string
	f    memFile
}

func (i *memInfo) Name() string       { return i.name }
func (i *memInfo) Size() int64        { return int64(len(i.f.data)) }
func (i *memInfo) ModTime() time.Time { return i.f.mtime }
func (i *memInfo) IsDir() bool        { return i.f.dir }
func (i *memInfo) Sys() any           { return nil }
func (i *memInfo) Mode() fs.FileMode {
	if i.f.dir {
		return fs.ModeDir | 0o755
	}
	return 0o644
}
