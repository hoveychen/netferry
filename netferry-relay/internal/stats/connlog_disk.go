package stats

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"

	"github.com/hoveychen/netferry/relay/internal/logfile"
)

// On-disk connection history: 4 files of up to 16 MB (~50k records each), a
// day or more of a busy machine.
const (
	connFileMaxSize = 16 * 1024 * 1024
	connFileBackups = 3
)

// connLogFile persists closed / failed connection records as JSON lines.
// logfile prefixes each line with a timestamp; readers skip to the first '{'.
type connLogFile struct {
	w    *logfile.RotatingWriter
	path string
}

// PersistConnLog appends every closed / failed connection to path (rotated
// into path.1..path.3) and makes /connections read its closed history from
// those files, so the history survives tunnel restarts and reaches back
// further than the in-memory ring. Call before ListenAndServe.
func (c *Counters) PersistConnLog(path string) error {
	w, err := logfile.New(path, connFileMaxSize, connFileBackups)
	if err != nil {
		return err
	}
	c.connFile = &connLogFile{w: w, path: path}
	return nil
}

func (c *Counters) persistConn(rec *ConnRecord) {
	if c.connFile == nil {
		return
	}
	b, err := json.Marshal(rec)
	if err != nil {
		return
	}
	c.connFile.w.Write(append(b, '\n'))
}

// files returns the existing history files, oldest first.
func (lf *connLogFile) files() []os.FileInfo {
	var out []os.FileInfo
	var paths []string
	for i := connFileBackups; i >= 1; i-- {
		paths = append(paths, fmt.Sprintf("%s.%d", lf.path, i))
	}
	paths = append(paths, lf.path)
	for _, p := range paths {
		if fi, err := os.Stat(p); err == nil {
			out = append(out, namedInfo{fi, p})
		}
	}
	return out
}

type namedInfo struct {
	os.FileInfo
	path string
}

// each calls fn for every record on disk that matches f, oldest file first,
// and returns the open time of the oldest record on disk. Files last written
// before f.sinceMs are skipped without reading: every record in them closed
// earlier.
func (lf *connLogFile) each(f connFilter, fn func(*ConnRecord)) (oldestMs int64, err error) {
	files := lf.files()
	if len(files) > 0 {
		oldestMs = firstOpenMs(files[0].(namedInfo).path)
	}
	hostNeedle := []byte(f.host)
	for _, fi := range files {
		if f.sinceMs > 0 && fi.ModTime().UnixMilli() < f.sinceMs {
			continue
		}
		fh, err := os.Open(fi.(namedInfo).path)
		if err != nil {
			return oldestMs, err
		}
		sc := bufio.NewScanner(fh)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			line := sc.Bytes()
			i := bytes.IndexByte(line, '{')
			if i < 0 {
				continue
			}
			line = line[i:]
			// Cheap rejections before paying for json.Unmarshal.
			if f.errOnly && !bytes.Contains(line, []byte(`"error":`)) {
				continue
			}
			if len(hostNeedle) > 0 && !bytes.Contains(bytes.ToLower(line), hostNeedle) {
				continue
			}
			var rec ConnRecord
			if json.Unmarshal(line, &rec) != nil {
				continue
			}
			if f.match(&rec) {
				fn(&rec)
			}
		}
		err = sc.Err()
		fh.Close()
		if err != nil {
			return oldestMs, err
		}
	}
	return oldestMs, nil
}

// firstOpenMs is the OpenedMs of the first parseable record in path, 0 if none.
func firstOpenMs(path string) int64 {
	fh, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer fh.Close()
	sc := bufio.NewScanner(fh)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if i := bytes.IndexByte(line, '{'); i >= 0 {
			var rec ConnRecord
			if json.Unmarshal(line[i:], &rec) == nil {
				return rec.OpenedMs
			}
		}
	}
	return 0
}
