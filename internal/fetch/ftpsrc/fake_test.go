package ftpsrc

import (
	"bufio"
	"net"
	"net/textproto"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeFile is one file served by fakeFTP. A stalling file sends its first
// stallAfter bytes and then blocks until the server shuts down.
type fakeFile struct {
	data       []byte
	mtime      time.Time
	stallAfter int
	denied     bool // RETR/SIZE answer 550 Permission denied
}

// fakeFTP is a minimal passive-mode FTP server (RFC 959 + SIZE/MDTM/EPSV)
// over loopback, enough to drive jlaffaye/ftp deterministically.
type fakeFTP struct {
	t    *testing.T
	ln   net.Listener
	user string
	pass string

	files map[string]fakeFile
	done  chan struct{}

	mu     sync.Mutex
	logins int
	conns  map[net.Conn]struct{}
	wg     sync.WaitGroup
}

func newFakeFTP(t *testing.T, files map[string]fakeFile) *fakeFTP {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeFTP{
		t: t, ln: ln, user: "xi", pass: "secret",
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

func (f *fakeFTP) config() Config {
	host, port, _ := net.SplitHostPort(f.ln.Addr().String())
	p, _ := strconv.Atoi(port)
	return Config{Name: "ftp-test", Host: host, Port: p, User: f.user, Password: f.pass, PoolSize: 1}
}

func (f *fakeFTP) loginCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.logins
}

// dropAll closes every control connection, like a server idle timeout.
func (f *fakeFTP) dropAll() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for c := range f.conns {
		_ = c.Close()
	}
}

func (f *fakeFTP) accept() {
	for {
		c, err := f.ln.Accept()
		if err != nil {
			return
		}
		f.mu.Lock()
		f.conns[c] = struct{}{}
		f.mu.Unlock()
		f.wg.Go(func() {
			defer func() {
				f.mu.Lock()
				delete(f.conns, c)
				f.mu.Unlock()
				_ = c.Close()
			}()
			f.serve(c)
		})
	}
}

func (f *fakeFTP) serve(c net.Conn) {
	tp := textproto.NewConn(c)
	reply := func(format string, args ...any) bool {
		return tp.PrintfLine(format, args...) == nil
	}
	if !reply("220 fake ready") {
		return
	}
	var user string
	loggedIn := false
	var pasv net.Listener
	defer func() {
		if pasv != nil {
			_ = pasv.Close()
		}
	}()
	for {
		line, err := tp.ReadLine()
		if err != nil {
			return
		}
		cmd, arg, _ := strings.Cut(line, " ")
		cmd = strings.ToUpper(cmd)
		if !loggedIn && cmd != "USER" && cmd != "PASS" && cmd != "QUIT" && cmd != "FEAT" {
			if !reply("530 Please login with USER and PASS.") {
				return
			}
			continue
		}
		var ok bool
		switch cmd {
		case "USER":
			user = arg
			if user == "blocked" {
				ok = reply("530 Non-anonymous sessions must use encryption.")
				break
			}
			ok = reply("331 Please specify the password.")
		case "PASS":
			if user == f.user && arg == f.pass {
				loggedIn = true
				f.mu.Lock()
				f.logins++
				f.mu.Unlock()
				ok = reply("230 Login successful.")
			} else {
				ok = reply("530 Login incorrect.")
			}
		case "FEAT":
			ok = reply("211-Features:\r\n SIZE\r\n MDTM\r\n EPSV\r\n211 End")
		case "TYPE":
			ok = reply("200 Switching to Binary mode.")
		case "SIZE":
			file, found := f.files[arg]
			switch {
			case !found:
				ok = reply("550 Could not get file size.")
			case file.denied:
				ok = reply("550 Permission denied.")
			default:
				ok = reply("213 %d", len(file.data))
			}
		case "MDTM":
			file, found := f.files[arg]
			if !found || file.mtime.IsZero() {
				ok = reply("550 Could not get file modification time.")
			} else {
				ok = reply("213 %s", file.mtime.UTC().Format("20060102150405"))
			}
		case "EPSV":
			if pasv != nil {
				_ = pasv.Close()
			}
			pasv, err = (&net.ListenConfig{}).Listen(f.t.Context(), "tcp", "127.0.0.1:0")
			if err != nil {
				return
			}
			_, port, _ := net.SplitHostPort(pasv.Addr().String())
			ok = reply("229 Entering Extended Passive Mode (|||%s|)", port)
		case "RETR":
			file, found := f.files[arg]
			if !found || pasv == nil {
				ok = reply("550 Failed to open file.")
				break
			}
			if !reply("150 Opening BINARY mode data connection.") {
				return
			}
			ok = f.send(pasv, file) && reply("226 Transfer complete.")
			_ = pasv.Close()
			pasv = nil
		case "QUIT":
			_ = reply("221 Goodbye.")
			return
		default:
			ok = reply("502 Command not implemented.")
		}
		if !ok {
			return
		}
	}
}

// send writes file over one accepted data connection.
func (f *fakeFTP) send(pasv net.Listener, file fakeFile) bool {
	dc, err := pasv.Accept()
	if err != nil {
		return false
	}
	defer dc.Close()
	w := bufio.NewWriter(dc)
	data := file.data
	if file.stallAfter > 0 {
		data = data[:file.stallAfter]
	}
	if _, err := w.Write(data); err != nil {
		return false
	}
	if err := w.Flush(); err != nil {
		return false
	}
	if file.stallAfter > 0 {
		// Block until shutdown or the client closes the data connection.
		closed := make(chan struct{})
		go func() {
			defer close(closed)
			var b [1]byte
			_, _ = dc.Read(b[:])
		}()
		select {
		case <-f.done:
		case <-closed:
		}
		return false
	}
	return true
}
