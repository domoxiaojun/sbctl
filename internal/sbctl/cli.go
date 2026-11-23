package sbctl

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"sbctl/contracts"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func stdin(limit int) []byte {
	b, e := io.ReadAll(io.LimitReader(os.Stdin, int64(limit+1)))
	check(e)
	if len(b) > limit {
		fail("INPUT_TOO_LARGE", "输入超过大小限制")
	}
	return b
}
func emit(v any) { _, e := os.Stdout.Write(append(canonical(v), '\n')); check(e) }
func argsFor(name string, args []string) *flag.FlagSet {
	f := flag.NewFlagSet(name, flag.ContinueOnError)
	f.SetOutput(io.Discard)
	return f
}
func parsed(f *flag.FlagSet, args []string, n int) {
	if err := f.Parse(args); err != nil || f.NArg() != n {
		fail("CLI_USAGE", "命令参数无效，请运行 help")
	}
}
func stopped(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return true
	case <-timer.C:
		return false
	}
}
func serving(ctx context.Context, address string, handler http.Handler) {
	server := &http.Server{Addr: address, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16384}
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			shutdown, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			defer cancel()
			_ = server.Shutdown(shutdown)
		case <-done:
		}
	}()
	err := server.ListenAndServe()
	close(done)
	if err != nil && err != http.ErrServerClosed {
		check(err)
	}
}

func ControllerMain(args []string) int {
	return mainBoundary(func(ctx context.Context) {
		if len(args) == 0 {
			args = []string{"serve"}
		}
		cmd, rest := args[0], args[1:]
		switch cmd {
		case "discovery-export":
			f := argsFor(cmd, rest)
			path := f.String("output", "/opt/sbctl/data/discovery/tailscale.json", "")
			parsed(f, rest, 0)
			DiscoveryExport(*path)
			return
		case "version":
			emit(M{"version": Version, "go": runtime.Version()})
			return
		case "help", "--help", "-h":
			fmt.Println("sbctl serve [--listen :8080] [--parallel 4] | subscriptions [--listen :8080] | initialize --password-stdin | initialize --password-file PATH | reset-password --password-stdin | reset-password --password-file PATH | register | import-user NAME GROUP REQUEST_KEY | create-user NAME GROUP | disable-user ID REVISION REASON | enable-user ID REVISION REASON REQUEST_KEY | preview-grants ID REVISION GROUPS | update-grants ID REVISION GROUPS REASON PREVIEW_HASH REQUEST_KEY | worker [--parallel N] | sync [--parallel N] | doctor | export-openapi PATH | acceptance --base-url URL --password-file PATH --group GROUP")
			return
		case "export-openapi":
			if len(rest) != 1 {
				fail("CLI_USAGE", "需要输出文件路径")
			}
			atomicWrite(rest[0], contracts.OpenAPI, 0644)
			return
		case "subscriptions":
			f := argsFor(cmd, rest)
			listen := f.String("listen", ":8080", "")
			parsed(f, rest, 0)
			serving(ctx, *listen, PublicAPI(env("SBCTL_PUBLIC_DIR", "/data/subscriptions")))
			return
		case "acceptance":
			Acceptance(rest)
			return
		}
		s := LoadSettings()
		switch cmd {
		case "initialize":
			f := argsFor(cmd, rest)
			name := f.String("login-name", "admin", "")
			fromStdin := f.Bool("password-stdin", false, "")
			file := f.String("password-file", "", "")
			parsed(f, rest, 0)
			if *fromStdin == (*file != "") {
				fail("CLI_USAGE", "选择 --password-stdin 或 --password-file")
			}
			if !exists(s.KeyFile) {
				CreateKey(s.KeyFile)
			}
			c := NewControl(s)
			c.Initialize()
			t := Traffic{s.TrafficDB, s.TrafficSnapshot}
			t.Initialize()
			password := ""
			if *fromStdin {
				password = strings.TrimSuffix(string(stdin(8192)), "\n")
			} else {
				if exists(*file) {
					fail("PASSWORD_FILE_EXISTS", "初始密码文件已存在")
				}
				password = token(24)
			}
			CreateAdmin(s, *name, "管理员", password)
			if *file != "" {
				atomicWrite(*file, []byte(password+"\n"), 0600)
			}
			fmt.Println("管理员与控制库已初始化")
		case "reset-password":
			f := argsFor(cmd, rest)
			name := f.String("login-name", "admin", "")
			fromStdin := f.Bool("password-stdin", false, "")
			file := f.String("password-file", "", "")
			parsed(f, rest, 0)
			if *fromStdin == (*file != "") {
				fail("CLI_USAGE", "选择 --password-stdin 或 --password-file")
			}
			password := ""
			if *fromStdin {
				password = strings.TrimSuffix(string(stdin(8192)), "\n")
			} else {
				if exists(*file) {
					fail("PASSWORD_FILE_EXISTS", "密码文件已存在，请指定新的路径")
				}
				password = token(24)
			}
			ResetAdminPassword(s, *name, password)
			if *file != "" {
				atomicWrite(*file, []byte(password+"\n"), 0600)
			}
			fmt.Println("管理员密码已重置，该账号的全部会话已失效")
		case "ledger-history", "register-replacement-ledger":
			t := Traffic{s.TrafficDB, s.TrafficSnapshot}
			t.Initialize()
			if cmd == "ledger-history" {
				parsed(argsFor(cmd, rest), rest, 1)
				emit(t.LedgerHistory(rest[0]))
				return
			}
			parsed(argsFor(cmd, rest), rest, 3)
			nodeID, previousID, expectedID := rest[0], rest[1], rest[2]
			validIdentity(nodeID)
			validIdentity(previousID)
			validIdentity(expectedID)
			c := NewControl(s)
			c.Initialize()
			var connection NodeConnection
			transaction(s.ControlDB, false, func(d *DB) {
				node := d.One("SELECT connection_json FROM nodes WHERE id=?", nodeID)
				if node == nil {
					fail("NOT_FOUND", "节点不存在", 404)
				}
				connection = parseConnection(object([]byte(str(node["connection_json"]))))
			})
			var snapshot Snapshot
			decode(canonical((AgentTransport{Control: c}).Call(connection, nil, true)), &snapshot)
			if snapshot.LedgerID != expectedID {
				fail("LEDGER_CHANGED", "节点控制通道返回的账本身份与指定的新身份不一致")
			}
			t.RegisterReplacement(nodeID, previousID, snapshot)
			emit(M{"node_id": nodeID, "previous_ledger_id": previousID, "ledger_id": expectedID})
		default:
			c := NewControl(s)
			c.Initialize()
			switch cmd {
			case "serve":
				f := argsFor(cmd, rest)
				listen := f.String("listen", ":8080", "")
				parallel := f.Int("parallel", 4, "")
				embeddedWorker := f.Bool("embedded-worker", true, "")
				parsed(f, rest, 0)
				if *parallel < 1 || *parallel > 32 {
					fail("CLI_USAGE", "并行数必须在 1 到 32 之间")
				}
				t := Traffic{s.TrafficDB, s.TrafficSnapshot}
				t.Initialize()
				_ = c.Doctor()
				if *embeddedWorker {
					w := NewWorker(c, nil)
					w.ExportInventory()
					go w.Run(ctx, *parallel)
				}
				serving(ctx, *listen, API(c))
			case "sync":
				f := argsFor(cmd, rest)
				parallel := f.Int("parallel", 4, "")
				parsed(f, rest, 0)
				if *parallel < 1 || *parallel > 32 {
					fail("CLI_USAGE", "并行数必须在 1 到 32 之间")
				}
				t := Traffic{s.TrafficDB, s.TrafficSnapshot}
				t.Initialize()
				tr := AgentTransport{Control: c}
				for {
					var nodes []any
					_ = Attempt(func() { nodes = array(object(read(s.InventoryFile))["nodes"]) })
					SyncNodes(ctx, t, tr, nodes, *parallel)
					if stopped(ctx, time.Duration(s.SyncSeconds)*time.Second) {
						return
					}
				}
			case "doctor":
				emit(c.Doctor())
			case "register":
				c.Register(object(stdin(65536)))
				fmt.Println("节点已登记")
			case "create-user":
				if len(rest) != 2 {
					fail("CLI_USAGE", "需要名称和节点组")
				}
				emit(c.CreateUser("maintenance", token(24), CreateUser{DisplayName: rest[0], GroupIDs: []string{rest[1]}}, nil))
			case "import-user":
				if len(rest) != 3 {
					fail("CLI_USAGE", "需要名称、节点组和请求标识")
				}
				emit(c.CreateUser("maintenance", rest[2], CreateUser{DisplayName: rest[0], GroupIDs: []string{rest[1]}, Note: "保留现有代理凭据接管"}, object(stdin(65536))))
			case "disable-user":
				if len(rest) != 3 {
					fail("CLI_USAGE", "需要用户标识、版本及原因")
				}
				rev := integer(rest[1])
				emit(c.DisableUser("maintenance", token(24), rest[0], DisableUser{&rev, rest[2]}))
			case "enable-user":
				if len(rest) != 4 {
					fail("CLI_USAGE", "需要用户标识、版本、原因和请求标识；重试沿用原请求标识")
				}
				rev, err := strconv.ParseInt(rest[1], 10, 64)
				if err != nil || rev < 0 {
					fail("CLI_USAGE", "用户版本必须为非负整数")
				}
				emit(c.EnableUser("maintenance", rest[3], rest[0], EnableUser{&rev, rest[2]}))
			case "preview-grants", "update-grants":
				expected := 3
				if cmd == "update-grants" {
					expected = 6
				}
				if len(rest) != expected {
					fail("CLI_USAGE", "预览需要用户标识、版本和逗号分隔节点组；提交另需原因、预览摘要和固定请求标识")
				}
				rev, err := strconv.ParseInt(rest[1], 10, 64)
				if err != nil || rev < 0 {
					fail("CLI_USAGE", "用户版本必须为非负整数")
				}
				selection := GrantSelection{&rev, parseGrantGroups(rest[2])}
				if cmd == "preview-grants" {
					emit(c.PreviewGrants(rest[0], selection))
				} else {
					emit(c.UpdateGrants("maintenance", rest[5], rest[0], UpdateGrants{selection, rest[3], rest[4]}))
				}
			case "worker":
				f := argsFor(cmd, rest)
				parallel := f.Int("parallel", 4, "")
				parsed(f, rest, 0)
				if *parallel < 1 || *parallel > 32 {
					fail("CLI_USAGE", "并行数必须在 1 到 32 之间")
				}
				w := NewWorker(c, nil)
				w.ExportInventory()
				w.Run(ctx, *parallel)
			default:
				fail("CLI_USAGE", "未知命令，请运行 help")
			}
		}
	})
}

func mainBoundary(fn func(context.Context)) int {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := Attempt(func() { fn(ctx) }); err != nil {
		f := PublicFault(err)
		fmt.Fprintln(os.Stderr, f.Code+": "+f.Message)
		return 1
	}
	return 0
}
