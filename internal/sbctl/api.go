package sbctl

import (
	"crypto/hmac"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"
)

const CookieName = "sbctl_session"

func jsonResponse(w http.ResponseWriter, status int, value any) {
	b := canonical(value)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}
func securityHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; font-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
}
func protect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		securityHeaders(w)
		err := Attempt(func() { next.ServeHTTP(w, r) })
		if err != nil {
			f := PublicFault(err)
			jsonResponse(w, f.Status, f)
		}
	})
}
func requestBody(w http.ResponseWriter, r *http.Request) []byte {
	r.Body = http.MaxBytesReader(w, r.Body, 32768)
	b, e := io.ReadAll(r.Body)
	if e != nil {
		fail("INPUT_TOO_LARGE", "请求超过大小限制", 413)
	}
	return b
}
func filters(r *http.Request) Filters {
	f := defaultsFilters()
	for k, v := range r.URL.Query() {
		if len(v) != 1 {
			fail("INVALID_REQUEST", "重复的筛选参数", 422)
		}
		switch k {
		case "q":
			f.Q = v[0]
		case "archived":
			if !contains([]string{"", "false", "true", "all"}, v[0]) {
				fail("INVALID_REQUEST", "归档筛选无效", 422)
			}
			f.Archived = v[0]
		case "status":
			f.Status = v[0]
		case "region":
			f.Region = v[0]
		case "sort":
			f.Sort = v[0]
		case "page", "page_size":
			n, e := strconv.Atoi(v[0])
			if e != nil {
				fail("INVALID_REQUEST", "分页参数无效", 422)
			}
			if k == "page" {
				f.Page = n
			} else {
				f.PageSize = n
			}
		default:
			fail("INVALID_REQUEST", "未知筛选参数", 422)
		}
	}
	if f.Page < 1 || f.Page > 100000 || f.PageSize < 1 || f.PageSize > 50 || utf8.RuneCountInString(f.Q) > 100 || utf8.RuneCountInString(f.Status) > 32 || utf8.RuneCountInString(f.Region) > 80 || !contains([]string{"name", "bytes", "created"}, f.Sort) {
		fail("INVALID_REQUEST", "筛选参数无效", 422)
	}
	return f
}
func cookie(r *http.Request) string {
	c, e := r.Cookie(CookieName)
	if e != nil {
		return ""
	}
	return c.Value
}
func origin(s Settings, r *http.Request) {
	if !contains(s.AllowedOrigins, r.Header.Get("Origin")) {
		fail("ORIGIN_REJECTED", "请求来源不被允许", 403)
	}
}
func API(c *Control) http.Handler {
	mux := http.NewServeMux()
	s := c.Settings
	v := Views{s}
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, 200, M{"status": "ok", "version": Version, "language": "go"})
	})
	mux.HandleFunc("POST /api/v1/session", func(w http.ResponseWriter, r *http.Request) {
		body := requestBody(w, r)
		var input struct {
			LoginName string `json:"login_name"`
			Password  string `json:"password"`
		}
		decode(body, &input)
		if utf8.RuneCountInString(input.LoginName) < 1 || utf8.RuneCountInString(input.LoginName) > 80 || utf8.RuneCountInString(input.Password) < 1 || utf8.RuneCountInString(input.Password) > 1024 {
			fail("INVALID_REQUEST", "请求参数不符合接口约定", 422)
		}
		origin(s, r)
		client, _, e := net.SplitHostPort(r.RemoteAddr)
		if e != nil {
			client = r.RemoteAddr
		}
		t, a := Login(s, input.LoginName, input.Password, client)
		http.SetCookie(w, &http.Cookie{Name: CookieName, Value: t, MaxAge: s.SessionSeconds, HttpOnly: true, Secure: s.SecureCookie, SameSite: http.SameSiteStrictMode, Path: "/"})
		a["csrf_token"] = c.Vault.CSRF(t)
		jsonResponse(w, 200, a)
	})
	auth := func(pattern string, fn func(http.ResponseWriter, *http.Request, M)) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			t := cookie(r)
			a := Authenticate(s, t)
			if r.Method != "GET" && r.Method != "HEAD" {
				origin(s, r)
				if !hmac.Equal([]byte(r.Header.Get("X-CSRF-Token")), []byte(c.Vault.CSRF(t))) {
					fail("CSRF_REJECTED", "会话校验失败，请刷新后重试", 403)
				}
			}
			fn(w, r, a)
		})
	}
	auth("GET /api/v1/session", func(w http.ResponseWriter, r *http.Request, a M) {
		a["csrf_token"] = c.Vault.CSRF(cookie(r))
		jsonResponse(w, 200, a)
	})
	auth("DELETE /api/v1/session", func(w http.ResponseWriter, r *http.Request, a M) {
		Logout(s, cookie(r))
		http.SetCookie(w, &http.Cookie{Name: CookieName, MaxAge: -1, HttpOnly: true, Secure: s.SecureCookie, SameSite: http.SameSiteStrictMode, Path: "/"})
		w.WriteHeader(204)
	})
	auth("GET /api/v1/overview", func(w http.ResponseWriter, r *http.Request, a M) { jsonResponse(w, 200, v.Overview()) })
	auth("GET /api/v1/traffic", func(w http.ResponseWriter, r *http.Request, a M) {
		jsonResponse(w, 200, v.TrafficReport(r.URL.Query()))
	})
	auth("GET /api/v1/node-discovery", func(w http.ResponseWriter, r *http.Request, a M) { jsonResponse(w, 200, c.Discovery()) })
	auth("GET /api/v1/node-onboarding", func(w http.ResponseWriter, r *http.Request, a M) { jsonResponse(w, 200, c.Onboarding()) })
	auth("POST /api/v1/node-jobs", func(w http.ResponseWriter, r *http.Request, a M) {
		var input NodeJobInput
		decode(requestBody(w, r), &input)
		jsonResponse(w, 202, c.SubmitNodeJob(str(a["admin_id"]), r.Header.Get("Idempotency-Key"), input))
	})
	auth("GET /api/v1/node-jobs/{id}", func(w http.ResponseWriter, r *http.Request, a M) { jsonResponse(w, 200, c.NodeJob(r.PathValue("id"))) })
	auth("POST /api/v1/node-jobs/{id}/retry", func(w http.ResponseWriter, r *http.Request, a M) {
		id := r.PathValue("id")
		transaction(s.ControlDB, true, func(d *DB) {
			row := d.One("SELECT state FROM node_jobs WHERE id=?", id)
			if row == nil {
				fail("NOT_FOUND", "节点任务不存在", 404)
			}
			if row["state"] == "failed" {
				d.Exec("UPDATE node_jobs SET state='queued',lease_until=0,error_code=NULL,message=NULL,updated_at=? WHERE id=?", now(), id)
				audit(d, str(a["admin_id"]), "retry_node_job", id, "重试原节点任务")
			}
		})
		jsonResponse(w, 202, M{"job_id": id})
	})
	auth("GET /api/v1/nodes/{id}/jobs", func(w http.ResponseWriter, r *http.Request, a M) { jsonResponse(w, 200, c.NodeJobs(r.PathValue("id"))) })
	auth("GET /api/v1/nodes/{id}/config", func(w http.ResponseWriter, r *http.Request, a M) {
		jsonResponse(w, 200, c.NodeConfig(r.PathValue("id")))
	})
	auth("POST /api/v1/nodes/{id}/metadata", func(w http.ResponseWriter, r *http.Request, a M) {
		var input struct {
			Draft   NodeDraft `json:"draft"`
			Version int64     `json:"version"`
		}
		decode(requestBody(w, r), &input)
		jsonResponse(w, 200, c.UpdateNodeLabels(str(a["admin_id"]), r.Header.Get("Idempotency-Key"), r.PathValue("id"), input.Version, input.Draft))
	})
	for _, route := range []struct {
		path string
		get  func() []M
	}{{"nodes", v.Nodes}, {"users", v.Users}, {"usage", v.Usage}, {"operations", v.Operations}, {"alerts", v.Alerts}} {
		auth("GET /api/v1/"+route.path, func(w http.ResponseWriter, r *http.Request, a M) {
			f := filters(r)
			jsonResponse(w, 200, Paginate(route.get(), f))
		})
	}
	for _, route := range []struct {
		path string
		get  func() []M
	}{{"nodes", v.Nodes}, {"users", v.Users}, {"operations", v.Operations}} {
		auth("GET /api/v1/"+route.path+"/{id}", func(w http.ResponseWriter, r *http.Request, a M) {
			jsonResponse(w, 200, one(route.get(), r.PathValue("id")))
		})
	}
	auth("GET /api/v1/groups", func(w http.ResponseWriter, r *http.Request, a M) { jsonResponse(w, 200, v.Groups()) })
	auth("POST /api/v1/users", func(w http.ResponseWriter, r *http.Request, a M) {
		var input CreateUser
		decode(requestBody(w, r), &input)
		jsonResponse(w, 202, c.CreateUser(str(a["admin_id"]), r.Header.Get("Idempotency-Key"), input, nil))
	})
	auth("POST /api/v1/users/{id}/metadata", func(w http.ResponseWriter, r *http.Request, a M) {
		var input UserMetadata
		decode(requestBody(w, r), &input)
		jsonResponse(w, 200, c.EditUser(str(a["admin_id"]), r.Header.Get("Idempotency-Key"), r.PathValue("id"), input))
	})
	auth("POST /api/v1/users/archive", func(w http.ResponseWriter, r *http.Request, a M) {
		var input ArchiveUsers
		decode(requestBody(w, r), &input)
		jsonResponse(w, 200, c.ArchiveUsers(str(a["admin_id"]), r.Header.Get("Idempotency-Key"), input))
	})
	auth("POST /api/v1/users/{id}/subscription-reset", func(w http.ResponseWriter, r *http.Request, a M) {
		var input ResetSubscription
		decode(requestBody(w, r), &input)
		jsonResponse(w, 200, c.ResetSubscription(str(a["admin_id"]), r.Header.Get("Idempotency-Key"), r.PathValue("id"), input))
	})
	auth("POST /api/v1/users/{id}/disable", func(w http.ResponseWriter, r *http.Request, a M) {
		var input DisableUser
		decode(requestBody(w, r), &input)
		jsonResponse(w, 202, c.DisableUser(str(a["admin_id"]), r.Header.Get("Idempotency-Key"), r.PathValue("id"), input))
	})
	auth("POST /api/v1/users/{id}/enable", func(w http.ResponseWriter, r *http.Request, a M) {
		var input EnableUser
		decode(requestBody(w, r), &input)
		jsonResponse(w, 202, c.EnableUser(str(a["admin_id"]), r.Header.Get("Idempotency-Key"), r.PathValue("id"), input))
	})
	auth("POST /api/v1/users/{id}/grants/preview", func(w http.ResponseWriter, r *http.Request, a M) {
		var input GrantSelection
		decode(requestBody(w, r), &input)
		jsonResponse(w, 200, c.PreviewGrants(r.PathValue("id"), input))
	})
	auth("POST /api/v1/users/{id}/grants", func(w http.ResponseWriter, r *http.Request, a M) {
		var input UpdateGrants
		decode(requestBody(w, r), &input)
		jsonResponse(w, 202, c.UpdateGrants(str(a["admin_id"]), r.Header.Get("Idempotency-Key"), r.PathValue("id"), input))
	})
	auth("GET /api/v1/users/{id}/audit", func(w http.ResponseWriter, r *http.Request, a M) {
		rows := []M{}
		transaction(s.ControlDB, false, func(d *DB) {
			userManagement(d, r.PathValue("id"))
			rows = d.Rows("SELECT action,summary,created_at FROM audit WHERE target_id=? ORDER BY id DESC LIMIT 30", r.PathValue("id"))
		})
		jsonResponse(w, 200, rows)
	})
	auth("GET /api/v1/users/{id}/subscription", func(w http.ResponseWriter, r *http.Request, a M) {
		jsonResponse(w, 200, v.Subscription(r.PathValue("id")))
	})
	auth("POST /api/v1/users/{id}/subscription-link", func(w http.ResponseWriter, r *http.Request, a M) {
		jsonResponse(w, 200, c.SubscriptionLink(str(a["admin_id"]), r.Header.Get("Idempotency-Key"), r.PathValue("id")))
	})
	auth("GET /api/v1/requests/{key}", func(w http.ResponseWriter, r *http.Request, a M) {
		jsonResponse(w, 200, c.FindRequest(str(a["admin_id"]), r.PathValue("key")))
	})
	auth("POST /api/v1/alerts/{id}/ack", func(w http.ResponseWriter, r *http.Request, a M) {
		jsonResponse(w, 200, c.Acknowledge(str(a["admin_id"]), r.Header.Get("Idempotency-Key"), r.PathValue("id")))
	})
	mux.Handle("GET /assets/", http.StripPrefix("/assets/", http.FileServer(http.Dir(filepath.Join(s.WebDir, "assets")))))
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/s/") {
			fail("NOT_FOUND", "页面不存在", 404)
		}
		path := filepath.Join(s.WebDir, "index.html")
		if r.URL.Path == "/favicon.svg" {
			path = filepath.Join(s.WebDir, "favicon.svg")
		}
		if _, e := os.Stat(path); e != nil {
			fail("NOT_FOUND", "页面不存在", 404)
		}
		http.ServeFile(w, r, path)
	})
	return protect(mux)
}
func PublicAPI(root string) http.Handler {
	store := PublicStore{root}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, 200, M{"status": "ok", "version": Version, "language": "go"})
	})
	mux.HandleFunc("GET /s/{token}/{filename}", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("filename")
		b := store.Read(r.PathValue("token"), name)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
		_, _ = w.Write(b)
	})
	return protect(mux)
}
