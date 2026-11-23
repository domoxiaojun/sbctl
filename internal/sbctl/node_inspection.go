package sbctl

import (
	"path/filepath"
	"strings"
)

func validateConfigDir(path string) {
	if path != "/etc/sing-box" && path != "/opt/sing-box" && !strings.HasPrefix(path, "/opt/sbctl/managed/") {
		fail("CONFIG_PATH", "配置目录须为 /etc/sing-box、/opt/sing-box 或 /opt/sbctl/managed 下的目录", 422)
	}
	if filepath.Clean(path) != path || strings.ContainsAny(path, "\r\n\x00") {
		fail("CONFIG_PATH", "配置路径无效", 422)
	}
}

func inspectionALPN(v any) []string {
	if value, ok := v.(string); ok {
		return []string{value}
	}
	return stringsOf(v)
}
