// Package sbctl implements the controller and the restricted node agent.
package sbctl

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

type M = map[string]any

const MaxDocument = 2 * 1024 * 1024
const Version = "0.10.0"

type Fault struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Status  int    `json:"-"`
}

func (f *Fault) Error() string { return f.Code + ": " + f.Message }
func fail(code, message string, status ...int) {
	s := 400
	if len(status) > 0 {
		s = status[0]
	}
	panic(&Fault{code, message, s})
}
func check(err error) {
	if err != nil {
		panic(err)
	}
}

// Attempt is the boundary for a complete operation. Database/file helpers abort
// by raising an error; deferred transaction cleanup always runs before recovery.
// Runtime panics are never disguised as a domain error.
func Attempt(fn func()) (err error) {
	defer func() {
		if p := recover(); p != nil {
			if _, ok := p.(runtime.Error); ok {
				panic(p)
			}
			if e, ok := p.(error); ok {
				err = e
			} else {
				panic(p)
			}
		}
	}()
	fn()
	return
}
func PublicFault(err error) *Fault {
	var f *Fault
	if errors.As(err, &f) {
		return f
	}
	return &Fault{"SERVICE_ERROR", "服务暂不可用，请查看运维状态", 503}
}
func now() string                     { return time.Now().UTC().Format("2006-01-02T15:04:05.000000Z") }
func timestamp() float64              { return float64(time.Now().UnixNano()) / 1e9 }
func random(n int) []byte             { b := make([]byte, n); _, err := rand.Read(b); check(err); return b }
func identifier(prefix string) string { return prefix + "_" + hex.EncodeToString(random(16)) }
func hash(b []byte) string            { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func canonical(v any) []byte {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	check(e.Encode(v))
	return bytes.TrimSuffix(b.Bytes(), []byte("\n"))
}
func digest(v any) string { return hash(canonical(v)) }
func decode(data []byte, target any) {
	if len(data) > MaxDocument {
		fail("INPUT_TOO_LARGE", "输入超过大小限制", 413)
	}
	if !utf8.Valid(data) {
		fail("INVALID_JSON", "JSON 编码无效")
	}
	// Reject duplicate keys at every nesting level before typed decoding.
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	checkJSON(d, 0)
	if _, err := d.Token(); err != io.EOF {
		fail("INVALID_JSON", "JSON 包含多余内容")
	}
	d = json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		fail("INVALID_REQUEST", "请求参数不符合接口约定", 422)
	}
}
func checkJSON(d *json.Decoder, depth int) {
	if depth > 100 {
		fail("INVALID_JSON", "JSON 嵌套过深")
	}
	t, err := d.Token()
	if err != nil {
		fail("INVALID_JSON", "JSON 格式无效")
	}
	delim, ok := t.(json.Delim)
	if !ok {
		return
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			k, e := d.Token()
			if e != nil {
				fail("INVALID_JSON", "JSON 格式无效")
			}
			s, ok := k.(string)
			if !ok || seen[s] {
				fail("INVALID_JSON", "JSON 包含重复字段")
			}
			seen[s] = true
			checkJSON(d, depth+1)
		}
	case '[':
		for d.More() {
			checkJSON(d, depth+1)
		}
	default:
		fail("INVALID_JSON", "JSON 格式无效")
	}
	_, err = d.Token()
	if err != nil {
		fail("INVALID_JSON", "JSON 格式无效")
	}
}
func object(data []byte) M {
	var m M
	decode(data, &m)
	if m == nil {
		fail("INVALID_JSON", "需要 JSON 对象")
	}
	return m
}
func read(path string) []byte {
	f, e := os.Open(path)
	check(e)
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, MaxDocument+1))
	check(e)
	if len(b) > MaxDocument {
		fail("INPUT_TOO_LARGE", "文件超过大小限制", 413)
	}
	return b
}
func atomicWrite(path string, data []byte, mode os.FileMode) {
	dir := filepath.Dir(path)
	check(os.MkdirAll(dir, 0755))
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".")
	check(err)
	defer os.Remove(f.Name())
	defer f.Close()
	check(f.Chmod(mode))
	_, err = f.Write(data)
	check(err)
	check(f.Sync())
	check(f.Close())
	check(os.Rename(f.Name(), path))
	d, err := os.Open(dir)
	check(err)
	defer d.Close()
	check(d.Sync())
}
func fileLock(path string, timeout time.Duration) func() {
	check(os.MkdirAll(filepath.Dir(path), 0755))
	f, e := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	check(e)
	deadline := time.Now().Add(timeout)
	for {
		e = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if e == nil {
			break
		}
		if !errors.Is(e, unix.EWOULDBLOCK) || time.Now().After(deadline) {
			f.Close()
			fail("LOCK_BUSY", "操作正在进行，请稍后重试", 503)
		}
		time.Sleep(25 * time.Millisecond)
	}
	return func() { _ = unix.Flock(int(f.Fd()), unix.LOCK_UN); _ = f.Close() }
}
func exists(path string) bool {
	_, e := os.Stat(path)
	if e != nil && !os.IsNotExist(e) {
		check(e)
	}
	return e == nil
}
func str(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}
func integer(v any) int64 {
	if v == nil {
		return 0
	}
	switch n := v.(type) {
	case int:
		return int64(n)
	case int64:
		return n
	case json.Number:
		r, e := n.Int64()
		check(e)
		return r
	}
	r, e := strconv.ParseInt(str(v), 10, 64)
	check(e)
	return r
}
func boolean(v any) bool { b, _ := v.(bool); return b }
func mapping(v any) M {
	m, _ := v.(map[string]any)
	if m == nil {
		return M{}
	}
	return m
}
func array(v any) []any {
	if v == nil {
		return []any{}
	}
	if a, ok := v.([]any); ok {
		return a
	}
	var a []any
	decode(canonical(v), &a)
	return a
}
func stringsOf(v any) []string {
	r := []string{}
	for _, s := range array(v) {
		r = append(r, str(s))
	}
	return r
}
func pick(m M, keys ...string) M {
	r := M{}
	for _, k := range keys {
		r[k] = m[k]
	}
	return r
}
func merge(a M, b M) M {
	for k, v := range b {
		a[k] = v
	}
	return a
}
func without(m M, keys ...string) M {
	r := M{}
	for k, v := range m {
		r[k] = v
	}
	for _, k := range keys {
		delete(r, k)
	}
	return r
}
func contains(a []string, s string) bool {
	for _, v := range a {
		if v == s {
			return true
		}
	}
	return false
}
func counter(v any) int64 {
	s := str(v)
	if s == "" || strings.Trim(s, "0123456789") != "" {
		fail("COUNTER_INVALID", "流量计数无效")
	}
	n, e := strconv.ParseInt(s, 10, 64)
	if e != nil || n < 0 {
		fail("COUNTER_INVALID", "流量计数超出范围")
	}
	return n
}
func stamp(s string) time.Time {
	t, e := time.Parse(time.RFC3339Nano, s)
	if e != nil {
		fail("SNAPSHOT_TIME", "时间格式无效")
	}
	return t
}
