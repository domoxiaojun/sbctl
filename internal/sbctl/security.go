package sbctl

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"golang.org/x/crypto/argon2"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

func token(n int) string { return base64.RawURLEncoding.EncodeToString(random(n)) }

type Vault struct {
	key  []byte
	aead cipher.AEAD
}

func CreateKey(path string) {
	check(os.MkdirAll(filepath.Dir(path), 0700))
	f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if os.IsExist(e) {
		fail("KEY_EXISTS", "加密密钥已存在，拒绝覆盖")
	}
	check(e)
	defer f.Close()
	_, e = f.Write([]byte(base64.RawURLEncoding.EncodeToString(random(32)) + "\n"))
	check(e)
	check(f.Sync())
}
func NewVault(path string) *Vault {
	k, e := base64.RawURLEncoding.DecodeString(strings.TrimSpace(string(read(path))))
	if e != nil || len(k) != 32 {
		fail("KEY_INVALID", "主密钥格式无效", 503)
	}
	b, e := aes.NewCipher(k)
	check(e)
	a, e := cipher.NewGCM(b)
	check(e)
	return &Vault{k, a}
}
func (v *Vault) Encrypt(value any) string {
	nonce := random(v.aead.NonceSize())
	data := v.aead.Seal(nonce, nonce, canonical(value), []byte("sbctl:v2"))
	return "v2:" + base64.RawURLEncoding.EncodeToString(data)
}
func (v *Vault) Decrypt(value string) any {
	if !strings.HasPrefix(value, "v2:") {
		fail("KEY_MISMATCH", "加密数据版本不匹配", 503)
	}
	data, e := base64.RawURLEncoding.DecodeString(value[3:])
	n := v.aead.NonceSize()
	if e != nil || len(data) < n {
		fail("KEY_MISMATCH", "密钥与数据不匹配", 503)
	}
	plain, e := v.aead.Open(nil, data[:n], data[n:], []byte("sbctl:v2"))
	if e != nil {
		fail("KEY_MISMATCH", "密钥与数据不匹配", 503)
	}
	var r any
	decode(plain, &r)
	return r
}
func (v *Vault) CSRF(t string) string {
	h := hmac.New(sha256.New, v.key)
	_, _ = h.Write([]byte("csrf:" + t))
	return fmt.Sprintf("%x", h.Sum(nil))
}
func passwordHash(password string) string {
	salt := random(16)
	key := argon2.IDKey([]byte(password), salt, 3, 64*1024, 4, 32)
	return "$argon2id$v=19$m=65536,t=3,p=4$" + base64.RawStdEncoding.EncodeToString(salt) + "$" + base64.RawStdEncoding.EncodeToString(key)
}
func passwordValid(encoded, password string) bool {
	p := strings.Split(encoded, "$")
	if len(p) != 6 || p[1] != "argon2id" || p[2] != "v=19" || p[3] != "m=65536,t=3,p=4" {
		return false
	}
	s, e := base64.RawStdEncoding.DecodeString(p[4])
	if e != nil || len(s) != 16 {
		return false
	}
	k, e := base64.RawStdEncoding.DecodeString(p[5])
	if e != nil || len(k) != 32 {
		return false
	}
	actual := argon2.IDKey([]byte(password), s, 3, 64*1024, 4, 32)
	return subtle.ConstantTimeCompare(k, actual) == 1
}

// Argon2id uses substantial memory. Limit concurrent hashes independently of
// the database-backed attempt budget, which is shared by API processes.
var loginSlots = make(chan struct{}, 2)

// Attempts are counted in a 15 minute window against three budgets. One source
// can spend only its own allowance on an account, so guessing from one address
// cannot lock the administrator out of every other address. The account-wide
// ceiling still bounds guessing that is spread over many sources.
const (
	loginWindow       = 900
	loginPairLimit    = 10
	loginSourceLimit  = 30
	loginAccountLimit = 100
)

// loginSource groups an IPv6 client with its /64, which a single subscriber controls.
func loginSource(client string) string {
	a, err := netip.ParseAddr(client)
	if err != nil {
		return client
	}
	if a = a.Unmap(); a.Is6() {
		if p, err := a.WithZone("").Prefix(64); err == nil {
			return p.String()
		}
	}
	return a.String()
}
func reserveLoginAttempt(s Settings, name, client string) []any {
	source := loginSource(client)
	budgets := []struct {
		subject string
		limit   int64
	}{
		{hash([]byte("login-pair:" + name + "\x00" + source)), loginPairLimit},
		{hash([]byte("login-source:" + source)), loginSourceLimit},
		{hash([]byte("login-account:" + name)), loginAccountLimit},
	}
	subjects := []any{}
	ts := timestamp()
	transaction(s.ControlDB, true, func(d *DB) {
		d.Exec("DELETE FROM login_failures WHERE attempted_at<?", ts-loginWindow)
		for _, b := range budgets {
			if integer(d.Value("SELECT count(*) FROM login_failures WHERE subject=?", b.subject)) >= b.limit {
				fail("LOGIN_RATE_LIMIT", "登录尝试过多，请稍后再试", 429)
			}
		}
		// Reserve before hashing so parallel requests cannot all pass the same limit.
		for _, b := range budgets {
			d.Exec("INSERT INTO login_failures VALUES (?,?)", b.subject, ts)
		}
	})
	for _, b := range budgets {
		subjects = append(subjects, b.subject)
	}
	return subjects
}
func CreateAdmin(s Settings, name, display, password string) string {
	if utf8.RuneCountInString(password) < 12 || utf8.RuneCountInString(password) > 1024 || utf8.RuneCountInString(name) < 1 || utf8.RuneCountInString(name) > 80 {
		fail("ADMIN_INVALID", "管理员密码至少需要 12 个字符")
	}
	h := passwordHash(password)
	id := identifier("adm")
	transaction(s.ControlDB, true, func(d *DB) {
		if d.One("SELECT 1 FROM admins LIMIT 1") != nil {
			fail("ADMIN_EXISTS", "管理员已经初始化", 409)
		}
		d.Exec("INSERT INTO admins(id,login_name,display_name,password_hash) VALUES (?,?,?,?)", id, name, display, h)
	})
	return id
}
func Login(s Settings, name, password, client string) (string, M) {
	select {
	case loginSlots <- struct{}{}:
		defer func() { <-loginSlots }()
	default:
		fail("LOGIN_BUSY", "登录请求繁忙，请稍后再试", 429)
	}
	subjects := reserveLoginAttempt(s, name, client)
	var admin M
	transaction(s.ControlDB, false, func(d *DB) {
		admin = d.One("SELECT * FROM admins WHERE login_name=? AND enabled=1", name)
	})
	valid := false
	if admin != nil {
		valid = passwordValid(str(admin["password_hash"]), password)
	} else {
		_ = passwordHash(password)
	}
	if !valid {
		fail("LOGIN_FAILED", "账号或密码错误", 401)
	}
	t := token(48)
	transaction(s.ControlDB, true, func(d *DB) {
		ts := timestamp()
		d.Exec("DELETE FROM sessions WHERE expires_at<?", ts)
		d.Exec("DELETE FROM login_failures WHERE subject IN (?,?,?)", subjects...)
		d.Exec("INSERT INTO sessions VALUES (?,?,?,?)", hash([]byte(t)), admin["id"], ts+float64(s.SessionSeconds), now())
	})
	return t, M{"admin_id": admin["id"], "display_name": admin["display_name"]}
}
func Authenticate(s Settings, t string) M {
	if t == "" || len(t) > 256 {
		fail("UNAUTHENTICATED", "请登录管理后台", 401)
	}
	var a M
	transaction(s.ControlDB, false, func(d *DB) {
		a = d.One("SELECT a.id,a.display_name FROM sessions s JOIN admins a ON a.id=s.admin_id WHERE s.token_hash=? AND s.expires_at>? AND a.enabled=1", hash([]byte(t)), timestamp())
	})
	if a == nil {
		fail("UNAUTHENTICATED", "请重新登录", 401)
	}
	return M{"admin_id": a["id"], "display_name": a["display_name"]}
}
func Logout(s Settings, t string) {
	transaction(s.ControlDB, true, func(d *DB) { d.Exec("DELETE FROM sessions WHERE token_hash=?", hash([]byte(t))) })
}
