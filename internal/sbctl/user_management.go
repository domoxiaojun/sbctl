package sbctl

import "strings"

type UserMetadata struct {
	Version     *int64 `json:"version"`
	DisplayName string `json:"display_name"`
	Note        string `json:"note"`
}
type ArchiveUsers struct {
	Users    []UserVersion `json:"users"`
	Archived bool          `json:"archived"`
}
type UserVersion struct {
	ID       string `json:"id"`
	Version  int64  `json:"version"`
	Revision int64  `json:"revision"`
}

func userManagement(d *DB, id string) M {
	u := d.One("SELECT u.*,COALESCE(m.version,0) AS metadata_version,m.archived_at FROM users u LEFT JOIN user_management m ON m.user_id=u.id WHERE u.id=?", id)
	if u == nil {
		fail("NOT_FOUND", "用户不存在", 404)
	}
	return u
}
func (c *Control) EditUser(actor, key, id string, req UserMetadata) M {
	if req.Version == nil || *req.Version < 0 {
		fail("INVALID_REQUEST", "缺少用户资料版本", 422)
	}
	validation := CreateUser{DisplayName: req.DisplayName, Note: req.Note, GroupIDs: []string{"validation"}}
	validation.Validate()
	req.DisplayName = validation.DisplayName
	path := "/users/" + id + "/metadata"
	var out M
	transaction(c.Settings.ControlDB, true, func(d *DB) {
		if out = c.replay(d, actor, key, path, req); out != nil {
			return
		}
		u := userManagement(d, id)
		if integer(u["metadata_version"]) != *req.Version {
			fail("STALE_REVISION", "用户资料已变化，请重新打开编辑", 409)
		}
		d.Exec("UPDATE users SET display_name=?,note=? WHERE id=?", req.DisplayName, req.Note, id)
		d.Exec("INSERT INTO user_management(user_id,version) VALUES (?,?) ON CONFLICT(user_id) DO UPDATE SET version=excluded.version", id, *req.Version+1)
		out = M{"saved": true, "version": *req.Version + 1}
		c.remember(d, actor, key, path, req, out, 200, false)
		audit(d, actor, "edit_user", id, "编辑用户名称与备注")
	})
	return out
}
func (c *Control) ArchiveUsers(actor, key string, req ArchiveUsers) M {
	if len(req.Users) < 1 || len(req.Users) > 50 {
		fail("INVALID_REQUEST", "每次请选择 1 至 50 位用户", 422)
	}
	path := "/users/archive"
	var out M
	transaction(c.Settings.ControlDB, true, func(d *DB) {
		if out = c.replay(d, actor, key, path, req); out != nil {
			return
		}
		seen := map[string]bool{}
		for _, item := range req.Users {
			if seen[item.ID] || item.Version < 0 || item.Revision < 0 {
				fail("INVALID_REQUEST", "用户选择重复或版本无效", 422)
			}
			seen[item.ID] = true
			u := userManagement(d, item.ID)
			if integer(u["metadata_version"]) != item.Version || integer(u["revision"]) != item.Revision {
				fail("STALE_REVISION", "用户状态或资料已变化，请刷新后重试", 409)
			}
			if req.Archived {
				if u["status"] != "disabled" || d.Value("SELECT state FROM subscriptions WHERE user_id=?", item.ID) != "revoked" || integer(d.Value("SELECT count(*) FROM credentials WHERE user_id=? AND revoked=0", item.ID)) > 0 {
					fail("ARCHIVE_NOT_READY", "请先禁用用户并完成订阅吊销后归档", 409)
				}
				if integer(d.Value("SELECT count(*) FROM outbox o JOIN operations p ON p.id=o.operation_id WHERE p.user_id=? AND o.state<>'done'", item.ID)) > 0 {
					fail("ARCHIVE_NOT_READY", "仍有节点撤销任务未完成，暂不能归档", 409)
				}
			}
		}
		for _, item := range req.Users {
			var archived any
			if req.Archived {
				archived = now()
			}
			d.Exec("INSERT INTO user_management(user_id,version,archived_at) VALUES (?,?,?) ON CONFLICT(user_id) DO UPDATE SET version=excluded.version,archived_at=excluded.archived_at", item.ID, item.Version+1, archived)
			action := "restore_user"
			if req.Archived {
				action = "archive_user"
			}
			audit(d, actor, action, item.ID, "更新用户归档状态，保留身份、授权历史和流量")
		}
		out = M{"saved": true, "count": len(req.Users)}
		c.remember(d, actor, key, path, req, out, 200, false)
	})
	return out
}

type ResetSubscription struct {
	Revision   *int64 `json:"revision"`
	Generation *int64 `json:"generation"`
	Reason     string `json:"reason"`
}

func (c *Control) ResetSubscription(actor, key, id string, req ResetSubscription) M {
	validation := EnableUser{req.Revision, req.Reason}
	validation.Validate()
	if req.Generation == nil || *req.Generation < 1 {
		fail("INVALID_REQUEST", "缺少订阅版本", 422)
	}
	path := "/users/" + id + "/subscription-reset"
	var out M
	unlock := c.Public.Lock()
	defer unlock()
	transaction(c.Settings.ControlDB, true, func(d *DB) {
		if out = c.replay(d, actor, key, path, req); out != nil {
			return
		}
		u := d.One("SELECT u.*,s.token_hash,s.generation,s.state FROM users u JOIN subscriptions s ON s.user_id=u.id WHERE u.id=?", id)
		if u == nil {
			fail("NOT_FOUND", "用户不存在", 404)
		}
		if integer(u["revision"]) != *req.Revision {
			fail("STALE_REVISION", "用户状态已变化，请刷新后重试", 409)
		}
		if integer(u["generation"]) != *req.Generation {
			fail("STALE_REVISION", "订阅已经重置，请刷新获取新链接", 409)
		}
		if u["status"] != "active" || u["state"] != "published" {
			fail("NOT_READY", "仅可重置已启用且已发布的订阅", 409)
		}
		if err := Attempt(func() { c.Public.Revoke(str(u["token_hash"])) }); err != nil {
			fail("SUBSCRIPTION_REVOKE_PENDING", "旧链接暂时无法吊销，请稍后重试", 409)
		}
		t := token(48)
		d.Exec("UPDATE subscriptions SET token_hash=?,token_ciphertext=?,generation=generation+1,state='pending' WHERE user_id=?", hash([]byte(t)), c.Vault.Encrypt(t), id)
		// Preserve user authorization revision: resetting a URL must not supersede grants.
		out = M{"saved": true, "generation": integer(u["generation"]) + 1}
		c.remember(d, actor, key, path, req, out, 200, false)
		audit(d, actor, "reset_subscription", id, strings.TrimSpace(req.Reason))
	})
	return out
}
